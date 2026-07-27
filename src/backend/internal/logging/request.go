package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	requestQueueSize = 1024
	minRequestBytes  = int64(1 << 20)
	maxRequestBytes  = int64(1024 << 20)
)

type RequestEntry struct {
	Timestamp     time.Time         `json:"timestamp"`
	RequestID     string            `json:"request_id"`
	Severity      Severity          `json:"severity"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Route         string            `json:"route"`
	Params        map[string]string `json:"params,omitempty"`
	Status        int               `json:"status"`
	DurationMS    int64             `json:"duration_ms"`
	ResponseBytes int64             `json:"response_bytes"`
	Operator      string            `json:"operator,omitempty"`
	IP            string            `json:"ip,omitempty"`
	UserAgent     string            `json:"user_agent,omitempty"`
	ErrorSummary  string            `json:"error_summary,omitempty"`
}

type RequestLogStatus struct {
	UsageBytes   int64  `json:"usage_bytes"`
	MaxBytes     int64  `json:"max_bytes"`
	Dropped      uint64 `json:"dropped"`
	Directory    string `json:"directory"`
	SegmentCount int    `json:"segment_count"`
}

type requestCommand struct {
	entry  *RequestEntry
	tail   int
	clear  bool
	max    int64
	status bool
	close  bool
	resp   chan requestResult
}

type requestResult struct {
	items  []RequestEntry
	status RequestLogStatus
	err    error
}

type RequestLog struct {
	dir            string
	commands       chan requestCommand
	done           chan struct{}
	closed         atomic.Bool
	dropped        atomic.Uint64
	pendingDropped atomic.Uint64
	reporter       atomic.Pointer[func(uint64, string)]
}

type requestWriter struct {
	owner    *RequestLog
	file     *os.File
	fileSize int64
	maxBytes int64
}

func OpenRequestLog(dir string, maxBytes int64) (*RequestLog, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create request log directory: %w", err)
	}
	log := &RequestLog{
		dir:      dir,
		commands: make(chan requestCommand, requestQueueSize),
		done:     make(chan struct{}),
	}
	writer := &requestWriter{owner: log, maxBytes: normalizeRequestBytes(maxBytes)}
	if err := writer.openCurrent(); err != nil {
		return nil, err
	}
	go writer.run()
	return log, nil
}

func (l *RequestLog) SetDropReporter(reporter func(uint64, string)) {
	if reporter == nil {
		l.reporter.Store(nil)
		return
	}
	l.reporter.Store(&reporter)
}

func (l *RequestLog) Append(entry RequestEntry) {
	if l.closed.Load() {
		l.dropped.Add(1)
		l.pendingDropped.Add(1)
		return
	}
	entry.Timestamp = entry.Timestamp.UTC()
	command := requestCommand{entry: &entry}
	select {
	case l.commands <- command:
	default:
		l.dropped.Add(1)
		l.pendingDropped.Add(1)
	}
}

func (l *RequestLog) Tail(ctx context.Context, limit int) ([]RequestEntry, RequestLogStatus, error) {
	if limit != 10 && limit != 30 && limit != 50 && limit != 100 {
		limit = 30
	}
	result, err := l.call(ctx, requestCommand{tail: limit})
	return result.items, result.status, err
}

func (l *RequestLog) Clear(ctx context.Context) error {
	result, err := l.call(ctx, requestCommand{clear: true})
	if err != nil {
		return err
	}
	return result.err
}

func (l *RequestLog) SetMaxBytes(ctx context.Context, maxBytes int64) error {
	result, err := l.call(ctx, requestCommand{max: normalizeRequestBytes(maxBytes)})
	if err != nil {
		return err
	}
	return result.err
}

func (l *RequestLog) Status(ctx context.Context) (RequestLogStatus, error) {
	result, err := l.call(ctx, requestCommand{status: true})
	return result.status, err
}

func (l *RequestLog) Close(ctx context.Context) error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	result, err := l.call(ctx, requestCommand{close: true})
	if err != nil {
		return err
	}
	select {
	case <-l.done:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *RequestLog) call(ctx context.Context, command requestCommand) (requestResult, error) {
	command.resp = make(chan requestResult, 1)
	select {
	case l.commands <- command:
	case <-ctx.Done():
		return requestResult{}, ctx.Err()
	case <-l.done:
		select {
		case result := <-command.resp:
			return result, result.err
		default:
			return requestResult{}, errors.New("request log is closed")
		}
	}
	select {
	case result := <-command.resp:
		return result, result.err
	case <-ctx.Done():
		return requestResult{}, ctx.Err()
	case <-l.done:
		return requestResult{}, errors.New("request log is closed")
	}
}

func (w *requestWriter) run() {
	defer close(w.owner.done)
	for command := range w.owner.commands {
		var result requestResult
		switch {
		case command.entry != nil:
			result.err = w.append(*command.entry)
			if result.err != nil {
				w.owner.dropped.Add(1)
				w.owner.pendingDropped.Add(1)
			} else if dropped := w.owner.pendingDropped.Swap(0); dropped > 0 {
				if reporter := w.owner.reporter.Load(); reporter != nil {
					(*reporter)(dropped, "请求日志队列已满或写入失败")
				}
			}
		case command.tail > 0:
			result.items, result.err = w.tail(command.tail)
			result.status, _ = w.status()
		case command.clear:
			result.err = w.clear()
		case command.max > 0:
			w.maxBytes = command.max
			result.err = w.rotateIfNeeded()
		case command.status:
			result.status, result.err = w.status()
		case command.close:
			if w.file != nil {
				result.err = w.file.Close()
			}
			command.resp <- result
			return
		}
		if command.resp != nil {
			command.resp <- result
		}
	}
}

func (w *requestWriter) append(entry RequestEntry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal request log: %w", err)
	}
	line = append(line, '\n')
	if w.fileSize > 0 && w.fileSize+int64(len(line)) > w.segmentLimit() {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	n, err := w.file.Write(line)
	w.fileSize += int64(n)
	if err != nil {
		return fmt.Errorf("append request log: %w", err)
	}
	return w.enforceLimit()
}

func (w *requestWriter) openCurrent() error {
	path := filepath.Join(w.owner.dir, "requests-current.jsonl")
	if err := repairTrailingLine(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open request log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("stat request log: %w", err)
	}
	w.file = file
	w.fileSize = info.Size()
	return nil
}

func (w *requestWriter) rotateIfNeeded() error {
	if w.fileSize > w.segmentLimit() {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	return w.enforceLimit()
}

func (w *requestWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close request log segment: %w", err)
		}
	}
	current := filepath.Join(w.owner.dir, "requests-current.jsonl")
	archived := filepath.Join(w.owner.dir, fmt.Sprintf("requests-%s-%d.jsonl",
		time.Now().UTC().Format("20060102T150405.000000000Z"), time.Now().UnixNano()))
	if w.fileSize > 0 {
		if err := os.Rename(current, archived); err != nil {
			return fmt.Errorf("rotate request log: %w", err)
		}
	} else {
		_ = os.Remove(current)
	}
	w.file = nil
	w.fileSize = 0
	return w.openCurrent()
}

func (w *requestWriter) enforceLimit() error {
	files, total, err := requestFiles(w.owner.dir)
	if err != nil {
		return err
	}
	current := filepath.Join(w.owner.dir, "requests-current.jsonl")
	for _, file := range files {
		if total <= w.maxBytes {
			break
		}
		if file.path == current {
			continue
		}
		if err := os.Remove(file.path); err != nil {
			return fmt.Errorf("remove old request log segment: %w", err)
		}
		total -= file.size
	}
	return nil
}

func (w *requestWriter) clear() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
	}
	w.file = nil
	files, _, err := requestFiles(w.owner.dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear request log: %w", err)
		}
	}
	w.fileSize = 0
	return w.openCurrent()
}

func (w *requestWriter) tail(limit int) ([]RequestEntry, error) {
	files, _, err := requestFiles(w.owner.dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name > files[j].name })
	currentName := "requests-current.jsonl"
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].name == currentName && files[j].name != currentName
	})
	items := make([]RequestEntry, 0, limit)
	for _, file := range files {
		lines, err := lastLines(file.path, limit-len(items))
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			var entry RequestEntry
			if json.Unmarshal(line, &entry) == nil {
				items = append(items, entry)
			}
			if len(items) == limit {
				return items, nil
			}
		}
	}
	return items, nil
}

func (w *requestWriter) status() (RequestLogStatus, error) {
	files, total, err := requestFiles(w.owner.dir)
	return RequestLogStatus{
		UsageBytes:   total,
		MaxBytes:     w.maxBytes,
		Dropped:      w.owner.dropped.Load(),
		Directory:    w.owner.dir,
		SegmentCount: len(files),
	}, err
}

func (w *requestWriter) segmentLimit() int64 {
	limit := w.maxBytes / 10
	if limit < 64<<10 {
		return 64 << 10
	}
	return limit
}

type requestFile struct {
	path string
	name string
	size int64
}

func requestFiles(dir string) ([]requestFile, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read request log directory: %w", err)
	}
	files := make([]requestFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "requests-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, err
		}
		file := requestFile{path: filepath.Join(dir, entry.Name()), name: entry.Name(), size: info.Size()}
		files = append(files, file)
		total += file.size
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, total, nil
}

func lastLines(path string, limit int) ([][]byte, error) {
	if limit <= 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return nil, err
	}
	const chunkSize int64 = 32 << 10
	var (
		offset = info.Size()
		data   []byte
	)
	for offset > 0 {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		data = append(chunk, data...)
		required := limit
		if offset > 0 {
			required++
		}
		if len(splitNonEmptyLines(data)) >= required || offset == 0 {
			break
		}
	}
	lines := splitNonEmptyLines(data)
	result := make([][]byte, 0, min(limit, len(lines)))
	for i := len(lines) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, lines[i])
	}
	return result, nil
}

func splitNonEmptyLines(data []byte) [][]byte {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	lines := make([][]byte, 0)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func repairTrailingLine(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open request log for repair: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	const chunkSize int64 = 32 << 10
	offset := info.Size()
	for offset > 0 {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if index := strings.LastIndexByte(string(chunk), '\n'); index >= 0 {
			return file.Truncate(offset + int64(index) + 1)
		}
	}
	return file.Truncate(0)
}

func normalizeRequestBytes(value int64) int64 {
	if value < minRequestBytes {
		return minRequestBytes
	}
	if value > maxRequestBytes {
		return maxRequestBytes
	}
	return value
}
