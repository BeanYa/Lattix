# CodeGraph Repository Analysis

- Generated: 2026-07-28 (Asia/Shanghai)
- CodeGraph: 1.0.1
- Query: `overall repository architecture entry points core modules request flow data flow and important call paths`
- Scope: up to 24 source files

## Index Status

```text
Project: /home/bean/workspace/Lattix-codex

Index Statistics:
  Files:     151
  Nodes:     2,471
  Edges:     6,775
  DB Size:   6.23 MB
  Backend:   node:sqlite - built-in (full WAL)
  Journal:   wal

Files by Language:
  go              89
  tsx             43
  typescript      14
  yaml             3
  javascript       2

Index is up to date.
```

## Exploration: overall repository architecture entry points core modules request flow data flow and important call paths

Found 134 symbols across 36 files.

### Blast radius — what depends on these (update/verify before editing)

- `RequestEntry` (src/backend/internal/logging/request.go:24) — 11 callers in `src/backend/internal/logging/http.go`, `src/backend/internal/logging/request.go`; tests: `src/backend/internal/logging/request_test.go`
- `Requester` (src/frontend/src/lib/requester.ts:66) — 1 caller in `src/frontend/src/lib/requester.ts`; ⚠️ no covering tests found

### Source Code

> The code below is the **verbatim, current on-disk source** of these files — re-read from disk on this call and line-numbered, byte-for-byte identical to what the Read tool returns. It is NOT a summary, outline, or stale cache. Treat each block as a Read you have already performed: do not Read a file shown here.

#### src/backend/internal/logging/request.go — requestCommand(instantiates), RequestEntry(references), requestResult(instantiates), requestFiles(calls), openCurrent(calls), Load(calls), +56 more

```go
21		maxRequestBytes  = int64(1024 << 20)
22	)
23	
24	type RequestEntry struct {
25		Timestamp           time.Time         `json:"timestamp"`
26		RequestID           string            `json:"request_id"`
27		TraceID             string            `json:"trace_id"`
28		Severity            Severity          `json:"severity"`
29		Transport           string            `json:"transport"`
30		Method              string            `json:"method,omitempty"`
31		Path                string            `json:"path,omitempty"`
32		Route               string            `json:"route,omitempty"`
33		RPCType             string            `json:"rpc_type,omitempty"`
34		Attributes          map[string]string `json:"attributes,omitempty"`
35		HTTPStatus          int               `json:"http_status,omitempty"`
36		RPCCode             string            `json:"rpc_code,omitempty"`
37		DurationMS          int64             `json:"duration_ms"`
38		ResponseBytes       int64             `json:"response_bytes"`
39		Operator            string            `json:"operator,omitempty"`
40		IP                  string            `json:"ip,omitempty"`
41		UserAgent           string            `json:"user_agent,omitempty"`
42		ErrorSummary        string            `json:"error_summary,omitempty"`
43		IdempotencyReplayed bool              `json:"idempotency_replayed,omitempty"`
44	}
45	
46	type RequestLogStatus struct {
47		UsageBytes   int64  `json:"usage_bytes"`
48		MaxBytes     int64  `json:"max_bytes"`
49		Dropped      uint64 `json:"dropped"`
50		Directory    string `json:"directory"`
51		SegmentCount int    `json:"segment_count"`
52	}
53	
54	type requestCommand struct {
55		entry  *RequestEntry
56		tail   int
57		clear  bool
58		max    int64
59		status bool
60		close  bool
61		resp   chan requestResult
62	}
63	
64	type requestResult struct {
65		items  []RequestEntry
66		status RequestLogStatus
67		err    error
68	}
69	
70	type RequestLog struct {
71		dir            string
72		commands       chan requestCommand
73		done           chan struct{}
74		closed         atomic.Bool
75		dropped        atomic.Uint64
76		pendingDropped atomic.Uint64
77		reporter       atomic.Pointer[func(uint64, string)]
78	}
79	
80	type requestWriter struct {
81		owner    *RequestLog
82		file     *os.File
83		fileSize int64
84		maxBytes int64
85	}
86	
87	func OpenRequestLog(dir string, maxBytes int64) (*RequestLog, error) {
88		if err := os.MkdirAll(dir, 0o700); err != nil {
89			return nil, fmt.Errorf("create request log directory: %w", err)
90		}
91		log := &RequestLog{
92			dir:      dir,
93			commands: make(chan requestCommand, requestQueueSize),
94			done:     make(chan struct{}),
95		}
96		writer := &requestWriter{owner: log, maxBytes: normalizeRequestBytes(maxBytes)}
97		if err := writer.openCurrent(); err != nil {
98			return nil, err
99		}
100		go writer.run()
101		return log, nil
102	}
103	
104	func (l *RequestLog) SetDropReporter(reporter func(uint64, string)) {
105		if reporter == nil {
106			l.reporter.Store(nil)
107			return
108		}
109		l.reporter.Store(&reporter)
110	}
111	
112	func (l *RequestLog) Append(entry RequestEntry) {
113		if l.closed.Load() {
114			l.dropped.Add(1)
115			l.pendingDropped.Add(1)
116			return
117		}
118		entry.Timestamp = entry.Timestamp.UTC()
119		command := requestCommand{entry: &entry}
120		select {
121		case l.commands <- command:
122		default:
123			l.dropped.Add(1)
124			l.pendingDropped.Add(1)
125		}
126	}
127	
128	func (l *RequestLog) Tail(ctx context.Context, limit int) ([]RequestEntry, RequestLogStatus, error) {
129		if limit != 10 && limit != 30 && limit != 50 && limit != 100 {
130			limit = 30
131		}
132		result, err := l.call(ctx, requestCommand{tail: limit})
133		return result.items, result.status, err
134	}
135	
136	func (l *RequestLog) Clear(ctx context.Context) error {
137		result, err := l.call(ctx, requestCommand{clear: true})
138		if err != nil {
139			return err
140		}
141		return result.err
142	}
143	
144	func (l *RequestLog) SetMaxBytes(ctx context.Context, maxBytes int64) error {
145		result, err := l.call(ctx, requestCommand{max: normalizeRequestBytes(maxBytes)})
146		if err != nil {
147			return err
148		}
149		return result.err
150	}
151	
152	func (l *RequestLog) Status(ctx context.Context) (RequestLogStatus, error) {
153		result, err := l.call(ctx, requestCommand{status: true})
154		return result.status, err
155	}
156	
157	func (l *RequestLog) Close(ctx context.Context) error {
158		if !l.closed.CompareAndSwap(false, true) {
159			return nil
160		}
161		result, err := l.call(ctx, requestCommand{close: true})
162		if err != nil {
163			return err
164		}
165		select {
166		case <-l.done:
167			return result.err
168		case <-ctx.Done():
169			return ctx.Err()
170		}
171	}
172	
173	func (l *RequestLog) call(ctx context.Context, command requestCommand) (requestResult, error) {
174		command.resp = make(chan requestResult, 1)
175		select {
176		case l.commands <- command:
177		case <-ctx.Done():
178			return requestResult{}, ctx.Err()
179		case <-l.done:
180			select {
181			case result := <-command.resp:
182				return result, result.err
183			default:
184				return requestResult{}, errors.New("request log is closed")
185			}
186		}
187		select {
188		case result := <-command.resp:
189			return result, result.err
190		case <-ctx.Done():
191			return requestResult{}, ctx.Err()
192		case <-l.done:
193			return requestResult{}, errors.New("request log is closed")
194		}
195	}
196	
197	func (w *requestWriter) run() {
198		defer close(w.owner.done)
199		for command := range w.owner.commands {
200			var result requestResult
201			switch {
202			case command.entry != nil:
203				result.err = w.append(*command.entry)
204				if result.err != nil {
205					w.owner.dropped.Add(1)
206					w.owner.pendingDropped.Add(1)
207				} else if dropped := w.owner.pendingDropped.Swap(0); dropped > 0 {
208					if reporter := w.owner.reporter.Load(); reporter != nil {
209						(*reporter)(dropped, "请求日志队列已满或写入失败")
210					}
211				}
212			case command.tail > 0:
213				result.items, result.err = w.tail(command.tail)
214				result.status, _ = w.status()
215			case command.clear:
216				result.err = w.clear()
217			case command.max > 0:
218				w.maxBytes = command.max
219				result.err = w.rotateIfNeeded()
220			case command.status:
221				result.status, result.err = w.status()
222			case command.close:
223				if w.file != nil {
224					result.err = w.file.Close()
225				}
226				command.resp <- result
227				return
228			}
229			if command.resp != nil {
230				command.resp <- result
231			}
232		}
233	}
234	
235	func (w *requestWriter) append(entry RequestEntry) error {
236		line, err := json.Marshal(entry)
237		if err != nil {
238			return fmt.Errorf("marshal request log: %w", err)
239		}
240		line = append(line, '\n')
241		if w.fileSize > 0 && w.fileSize+int64(len(line)) > w.segmentLimit() {
242			if err := w.rotate(); err != nil {
243				return err
244			}
245		}
246		n, err := w.file.Write(line)
247		w.fileSize += int64(n)
248		if err != nil {
249			return fmt.Errorf("append request log: %w", err)
250		}
251		return w.enforceLimit()
252	}
253	
254	func (w *requestWriter) openCurrent() error {
255		path := filepath.Join(w.owner.dir, "requests-current.jsonl")
256		if err := repairTrailingLine(path); err != nil {
257			return err
258		}
259		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
260		if err != nil {
261			return fmt.Errorf("open request log: %w", err)
262		}
263		info, err := file.Stat()
264		if err != nil {
265			file.Close()
266			return fmt.Errorf("stat request log: %w", err)
267		}
268		w.file = file
269		w.fileSize = info.Size()
270		return nil
271	}
272	
273	func (w *requestWriter) rotateIfNeeded() error {
274		if w.fileSize > w.segmentLimit() {
275			if err := w.rotate(); err != nil {
276				return err
277			}
278		}
279		return w.enforceLimit()
280	}
281	
282	func (w *requestWriter) rotate() error {
283		if w.file != nil {
284			if err := w.file.Close(); err != nil {
285				return fmt.Errorf("close request log segment: %w", err)
286			}
287		}
288		current := filepath.Join(w.owner.dir, "requests-current.jsonl")
289		archived := filepath.Join(w.owner.dir, fmt.Sprintf("requests-%s-%d.jsonl",
290			time.Now().UTC().Format("20060102T150405.000000000Z"), time.Now().UnixNano()))
291		if w.fileSize > 0 {
292			if err := os.Rename(current, archived); err != nil {
293				return fmt.Errorf("rotate request log: %w", err)
294			}
295		} else {
296			_ = os.Remove(current)
297		}
298		w.file = nil
299		w.fileSize = 0
300		return w.openCurrent()
301	}
302	
303	func (w *requestWriter) enforceLimit() error {
304		files, total, err := requestFiles(w.owner.dir)
305		if err != nil {
306			return err
307		}
308		current := filepath.Join(w.owner.dir, "requests-current.jsonl")
309		for _, file := range files {
310			if total <= w.maxBytes {
311				break
312			}
313			if file.path == current {
314				continue
315			}
316			if err := os.Remove(file.path); err != nil {
317				return fmt.Errorf("remove old request log segment: %w", err)
318			}
319			total -= file.size
320		}
321		return nil
322	}
323	
324	func (w *requestWriter) clear() error {
325		if w.file != nil {
326			if err := w.file.Close(); err != nil {
327				return err
328			}
329		}
330		w.file = nil
331		files, _, err := requestFiles(w.owner.dir)
332		if err != nil {
333			return err
334		}
335		for _, file := range files {
336			if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
337				return fmt.Errorf("clear request log: %w", err)
338			}
339		}
340		w.fileSize = 0
341		return w.openCurrent()
342	}
343	
344	func (w *requestWriter) tail(limit int) ([]RequestEntry, error) {
345		files, _, err := requestFiles(w.owner.dir)
346		if err != nil {
347			return nil, err
348		}
349		sort.Slice(files, func(i, j int) bool { return files[i].name > files[j].name })
350		currentName := "requests-current.jsonl"
351		sort.SliceStable(files, func(i, j int) bool {
352			return files[i].name == currentName && files[j].name != currentName
353		})
354		items := make([]RequestEntry, 0, limit)
355		for _, file := range files {
356			lines, err := lastLines(file.path, limit-len(items))
357			if err != nil {
358				return nil, err
359			}
360			for _, line := range lines {
361				var entry RequestEntry
362				if json.Unmarshal(line, &entry) == nil {
363					items = append(items, entry)
364				}
365				if len(items) == limit {
366					return items, nil
367				}
368			}
369		}
370		return items, nil
371	}
372	
373	func (w *requestWriter) status() (RequestLogStatus, error) {
374		files, total, err := requestFiles(w.owner.dir)
375		return RequestLogStatus{
376			UsageBytes:   total,
377			MaxBytes:     w.maxBytes,
378			Dropped:      w.owner.dropped.Load(),
379			Directory:    w.owner.dir,
380			SegmentCount: len(files),
381		}, err
382	}
383	
384	func (w *requestWriter) segmentLimit() int64 {
385		limit := w.maxBytes / 10
386		if limit < 64<<10 {
387			return 64 << 10
388		}
389		return limit
390	}
391	
392	type requestFile struct {
393		path string
394		name string
395		size int64
396	}
397	
398	func requestFiles(dir string) ([]requestFile, int64, error) {
399		entries, err := os.ReadDir(dir)
400		if err != nil {
401			return nil, 0, fmt.Errorf("read request log directory: %w", err)
402		}
403		files := make([]requestFile, 0, len(entries))
404		var total int64
405		for _, entry := range entries {
406			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "requests-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
407				continue
408			}
409			info, err := entry.Info()
410			if err != nil {
411				return nil, 0, err
412			}
413			file := requestFile{path: filepath.Join(dir, entry.Name()), name: entry.Name(), size: info.Size()}
414			files = append(files, file)
415			total += file.size
416		}
417		sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
418		return files, total, nil
419	}
420	
421	func lastLines(path string, limit int) ([][]byte, error) {
422		if limit <= 0 {
423			return nil, nil
424		}
425		file, err := os.Open(path)
426		if err != nil {
427			return nil, err
428		}
429		defer file.Close()
430		info, err := file.Stat()
431		if err != nil || info.Size() == 0 {
432			return nil, err
433		}
434		const chunkSize int64 = 32 << 10
435		var (
436			offset = info.Size()
437			data   []byte
438		)
439		for offset > 0 {
440			readSize := chunkSize
441			if offset < readSize {
442				readSize = offset
443			}
444			offset -= readSize
445			chunk := make([]byte, readSize)
446			if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
447				return nil, err
448			}
449			data = append(chunk, data...)
450			required := limit
451			if offset > 0 {
452				required++
453			}
454			if len(splitNonEmptyLines(data)) >= required || offset == 0 {
455				break
456			}
457		}
458		lines := splitNonEmptyLines(data)
459		result := make([][]byte, 0, min(limit, len(lines)))
460		for i := len(lines) - 1; i >= 0 && len(result) < limit; i-- {
461			result = append(result, lines[i])
462		}
463		return result, nil
464	}
465	
466	func splitNonEmptyLines(data []byte) [][]byte {
467		scanner := bufio.NewScanner(strings.NewReader(string(data)))
468		scanner.Buffer(make([]byte, 4096), 64<<10)
469		lines := make([][]byte, 0)
470		for scanner.Scan() {
471			line := append([]byte(nil), scanner.Bytes()...)
472			if len(line) > 0 {
473				lines = append(lines, line)
474			}
475		}
476		return lines
477	}
478	
479	func repairTrailingLine(path string) error {
480		file, err := os.OpenFile(path, os.O_RDWR, 0o600)
481		if errors.Is(err, os.ErrNotExist) {
482			return nil
483		}
484		if err != nil {
485			return fmt.Errorf("open request log for repair: %w", err)
486		}
487		defer file.Close()
488		info, err := file.Stat()
489		if err != nil || info.Size() == 0 {
490			return err
491		}
492		last := make([]byte, 1)
493		if _, err := file.ReadAt(last, info.Size()-1); err != nil {
494			return err
495		}
496		if last[0] == '\n' {
497			return nil
498		}
499		const chunkSize int64 = 32 << 10
500		offset := info.Size()
501		for offset > 0 {
502			readSize := chunkSize
503			if offset < readSize {
504				readSize = offset
505			}
506			offset -= readSize
507			chunk := make([]byte, readSize)
508			if _, err := file.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
509				return err
510			}
511			if index := strings.LastIndexByte(string(chunk), '\n'); index >= 0 {
512				return file.Truncate(offset + int64(index) + 1)
513			}
514		}
515		return file.Truncate(0)
516	}
517	
518	func normalizeRequestBytes(value int64) int64 {
519		if value < minRequestBytes {
520			return minRequestBytes
521		}
522		if value > maxRequestBytes {
523			return maxRequestBytes
524		}
525		return value
526	}
527	
```

#### src/agent/internal/xray/hot.go — call(method), HotClient(struct), callConn(method), QueryStats(method), ReplaceInbound(method), RemoveInbound(method), +5 more

```go
1	package xray
2	
3	import (
4		"context"
5		"encoding/json"
6		"fmt"
7		"time"
8	
9		"google.golang.org/grpc"
10		"google.golang.org/grpc/credentials/insecure"
11	
12		"github.com/xtls/xray-core/app/proxyman/command"
13		statscommand "github.com/xtls/xray-core/app/stats/command"
14		"github.com/xtls/xray-core/common/protocol"
15		"github.com/xtls/xray-core/common/serial"
16		"github.com/xtls/xray-core/infra/conf"
17		"github.com/xtls/xray-core/proxy/trojan"
18		"github.com/xtls/xray-core/proxy/vless"
19		"github.com/xtls/xray-core/proxy/vmess"
20	
21		"lattix/shared"
22	)
23	
24	// rpcTimeout 是单次 gRPC 调用的超时。
25	const rpcTimeout = 5 * time.Second
26	
27	// HotClient 是 xray gRPC API 客户端（§6 热操作主路径）：
28	// AddInbound / AlterInbound（AddUser/RemoveUserOperation，零重启增删用户）/ RemoveInbound，
29	// 以及 StatsService 流量计数器查询（§13 遥测）。
30	type HotClient struct {
31		addr string
32	}
33	
34	// NewHotClient 创建指向 api inbound（dokodemo-door）的客户端。
35	func NewHotClient(addr string) *HotClient {
36		return &HotClient{addr: addr}
37	}
38	
39	func (c *HotClient) callConn(fn func(ctx context.Context, conn *grpc.ClientConn) error) error {
40		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
41		defer cancel()
42		conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
43		if err != nil {
44			return err
45		}
46		defer conn.Close()
47		return fn(ctx, conn)
48	}
49	
50	func (c *HotClient) call(fn func(ctx context.Context, h command.HandlerServiceClient) error) error {
51		return c.callConn(func(ctx context.Context, conn *grpc.ClientConn) error {
52			return fn(ctx, command.NewHandlerServiceClient(conn))
53		})
54	}
55	
56	// QueryStats 拉取 xray 全部流量计数器（计数器名 → 自 xray 启动的累计值，§13）。
57	func (c *HotClient) QueryStats() (map[string]int64, error) {
58		out := map[string]int64{}
59		err := c.callConn(func(ctx context.Context, conn *grpc.ClientConn) error {
60			resp, err := statscommand.NewStatsServiceClient(conn).QueryStats(ctx, &statscommand.QueryStatsRequest{})
61			if err != nil {
62				return err
63			}
64			for _, s := range resp.GetStat() {
65				out[s.GetName()] = s.GetValue()
66			}
67			return nil
68		})
69		return out, err
70	}
71	
72	// ReplaceInbound 幂等地下发 inbound：先移除同 tag 旧 inbound（不存在属预期），再添加。
73	// inbound 为填充后的 xray inbound JSON，经 infra/conf 转为 protobuf。
74	func (c *HotClient) ReplaceInbound(tag string, inbound json.RawMessage) error {
75		return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
76			_, _ = h.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag})
77			var ic conf.InboundDetourConfig
78			if err := json.Unmarshal(inbound, &ic); err != nil {
79				return fmt.Errorf("解析 inbound 配置: %w", err)
80			}
81			pb, err := ic.Build()
82			if err != nil {
83				return fmt.Errorf("构建 inbound 配置: %w", err)
84			}
85			if _, err := h.AddInbound(ctx, &command.AddInboundRequest{Inbound: pb}); err != nil {
86				return fmt.Errorf("AddInbound: %w", err)
87			}
88			return nil
89		})
90	}
91	
92	// RemoveInbound 热删除一个 inbound。
93	func (c *HotClient) RemoveInbound(tag string) error {
94		return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
95			if _, err := h.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag}); err != nil {
96				return fmt.Errorf("RemoveInbound: %w", err)
97			}
98			return nil
99		})
100	}
101	
102	// AddUser 向指定 inbound 热加入一个用户（AlterInbound AddUserOperation，零重启，§6）。
103	// 仅 vless/vmess/trojan 支持热操作；其余协议返回错误，由 Manager 回退重启兜底。
104	func (c *HotClient) AddUser(tag string, p shared.UserNodeParams, uuid string) error {
105		return c.alterUser(tag, p, uuid, true)
106	}
107	
108	// RemoveUser 从指定 inbound 热移除一个用户（RemoveUserOperation 按 email 匹配）。
109	func (c *HotClient) RemoveUser(tag string, p shared.UserNodeParams, uuid string) error {
110		return c.alterUser(tag, p, uuid, false)
111	}
112	
113	func (c *HotClient) alterUser(tag string, p shared.UserNodeParams, uuid string, add bool) error {
114		if p.Protocol != shared.ProtocolVLESS &&
115			p.Protocol != shared.ProtocolVMess && p.Protocol != shared.ProtocolTrojan {
116			return fmt.Errorf("协议 %s 不支持热操作用户", p.Protocol)
117		}
118		return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
119			var op *serial.TypedMessage
120			if add {
121				op = serial.ToTypedMessage(&command.AddUserOperation{
122					User: &protocol.User{
123						Email:   uuid,
124						Account: userAccount(p, uuid),
125					},
126				})
127			} else {
128				op = serial.ToTypedMessage(&command.RemoveUserOperation{Email: uuid})
129			}
130			if _, err := h.AlterInbound(ctx, &command.AlterInboundRequest{Tag: tag, Operation: op}); err != nil {
131				return fmt.Errorf("AlterInbound(%s): %w", tag, err)
132			}
133			return nil
134		})
135	}
136	
137	// userAccount 按协议构造热加用户的 account（与 config 文件中的用户条目一致）。
138	func userAccount(p shared.UserNodeParams, uuid string) *serial.TypedMessage {
139		switch p.Protocol {
140		case shared.ProtocolVMess:
141			return serial.ToTypedMessage(&vmess.Account{Id: uuid})
142		case shared.ProtocolTrojan:
143			return serial.ToTypedMessage(&trojan.Account{Password: uuid})
144		default: // vless
145			return serial.ToTypedMessage(&vless.Account{Id: uuid, Flow: p.Flow})
146		}
147	}
```


... (output truncated to budget; the source above is complete and verbatim — treat it as already Read. For any area not covered, run another codegraph_explore with the specific names — do NOT Read these files.)
