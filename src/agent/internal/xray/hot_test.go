package xray

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	statscommand "github.com/xtls/xray-core/app/stats/command"

	"lattix/shared"
)

// fakeStatsClient 是 statscommand.StatsServiceClient 的测试替身：
// 嵌入接口满足其余方法，仅实现回退路径用到的两个 RPC。
type fakeStatsClient struct {
	statscommand.StatsServiceClient
	users []string
	ips   map[string]map[string]int64
	errs  map[string]error
}

func (f *fakeStatsClient) GetAllOnlineUsers(ctx context.Context, in *statscommand.GetAllOnlineUsersRequest, opts ...grpc.CallOption) (*statscommand.GetAllOnlineUsersResponse, error) {
	if err := f.errs["users"]; err != nil {
		return nil, err
	}
	return &statscommand.GetAllOnlineUsersResponse{Users: f.users}, nil
}

func (f *fakeStatsClient) GetStatsOnlineIpList(ctx context.Context, in *statscommand.GetStatsRequest, opts ...grpc.CallOption) (*statscommand.GetStatsOnlineIpListResponse, error) {
	if err := f.errs[in.Name]; err != nil {
		return nil, err
	}
	return &statscommand.GetStatsOnlineIpListResponse{Name: in.Name, Ips: f.ips[in.Name]}, nil
}

func TestQueryOnlineUsersLegacyHappyPath(t *testing.T) {
	fake := &fakeStatsClient{
		users: []string{
			"user>>>11111111-2222-3333-4444-555555555555>>>online",
			"user>>>66666666-7777-8888-9999-000000000000>>>online",
		},
		ips: map[string]map[string]int64{
			"user>>>11111111-2222-3333-4444-555555555555>>>online": {"1.2.3.4": 1, "5.6.7.8": 2},
			"user>>>66666666-7777-8888-9999-000000000000>>>online": {"9.9.9.9": 3},
		},
	}
	users, err := queryOnlineUsersLegacy(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users: %+v", len(users), users)
	}
	byUser := map[string]shared.OnlineUserStat{}
	for _, u := range users {
		byUser[u.User] = u
	}
	first := byUser["11111111-2222-3333-4444-555555555555"]
	if len(first.IPs) != 2 {
		t.Fatalf("first user ips = %v", first.IPs)
	}
	second := byUser["66666666-7777-8888-9999-000000000000"]
	if len(second.IPs) != 1 || second.IPs[0] != "9.9.9.9" {
		t.Fatalf("second user ips = %v", second.IPs)
	}
}

func TestQueryOnlineUsersLegacySkipsFailedUsers(t *testing.T) {
	fake := &fakeStatsClient{
		users: []string{
			"user>>>aaa-bbb-ccc-ddd-eee111111111>>>online",
			"user>>>aaa-bbb-ccc-ddd-eee222222222>>>online",
		},
		ips: map[string]map[string]int64{
			"user>>>aaa-bbb-ccc-ddd-eee111111111>>>online": {"1.1.1.1": 1},
		},
		errs: map[string]error{
			"user>>>aaa-bbb-ccc-ddd-eee222222222>>>online": errors.New("gone"),
		},
	}
	users, err := queryOnlineUsersLegacy(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].User != "aaa-bbb-ccc-ddd-eee111111111" {
		t.Fatalf("got %+v, want only first user", users)
	}
}

func TestQueryOnlineUsersLegacySkipsEmptyEmailKeys(t *testing.T) {
	fake := &fakeStatsClient{
		users: []string{
			"user>>>real-user>>>online",
			"user>>>>>>online",
			"not-a-map-key",
		},
		ips: map[string]map[string]int64{
			"user>>>real-user>>>online": {"8.8.8.8": 1},
		},
	}
	users, err := queryOnlineUsersLegacy(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].User != "real-user" {
		t.Fatalf("got %+v, want only real-user", users)
	}
}

func TestQueryOnlineUsersLegacyAllUsersError(t *testing.T) {
	fake := &fakeStatsClient{errs: map[string]error{"users": errors.New("unavailable")}}
	if _, err := queryOnlineUsersLegacy(context.Background(), fake); err == nil {
		t.Fatal("expected error from GetAllOnlineUsers to propagate")
	}
}

func TestQueryOnlineUsersLegacyEmptyOnlineSet(t *testing.T) {
	fake := &fakeStatsClient{}
	users, err := queryOnlineUsersLegacy(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if users == nil || len(users) != 0 {
		t.Fatalf("got %+v, want empty non-nil slice", users)
	}
}

func TestQueryOnlineUsersLegacyStopsOnCancelledContext(t *testing.T) {
	fake := &fakeStatsClient{users: []string{"user>>>a-b-c-d-e111111111111>>>online"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	users, err := queryOnlineUsersLegacy(ctx, fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("got %+v, want no users after cancellation", users)
	}
}
