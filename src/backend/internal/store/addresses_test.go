package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateServerAddressesBackfill 验证 v16 库迁移到 v17 时：存量服务器的当前
// 公网地址回填为地址列表唯一条目（默认地址仍由 address 列表达），且幂等。
func TestMigrateServerAddressesBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addresses.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	// 模拟 v16 库：servers 无 addresses 列，chain_hops 无 address 列。
	if _, err := db.Exec(`ALTER TABLE servers DROP COLUMN addresses`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE chain_hops DROP COLUMN address`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (alias, token, address, address_mode) VALUES
		('a', 'tok-a', '1.2.3.4', 'manual'), ('b', 'tok-b', 'v6.example.com', 'manual'), ('c', 'tok-c', '', 'auto')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 16`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	servers, err := st.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": `["1.2.3.4"]`, "b": `["v6.example.com"]`, "c": ""}
	for _, srv := range servers {
		w, ok := want[srv.Alias]
		if !ok {
			continue
		}
		if srv.Addresses != w {
			t.Errorf("server %s addresses = %q, want %q", srv.Alias, srv.Addresses, w)
		}
	}
	st.Close()

	// 幂等：再次打开不重复回填/报错。
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	srv, err := st2.ServerByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Addresses != `["1.2.3.4"]` {
		t.Fatalf("reopen addresses = %q, want [\"1.2.3.4\"]", srv.Addresses)
	}
}

func TestResolveServerAddress(t *testing.T) {
	srv := &Server{
		Address:     "1.2.3.4",
		LearnedAddr: "1.2.3.4",
		Addresses:   `["1.2.3.4","2400:cb00::1","example.com"]`,
	}
	cases := []struct {
		selected string
		want     string
	}{
		{"", "1.2.3.4"},                    // 空 = 跟随默认
		{"2400:cb00::1", "2400:cb00::1"},   // 列表条目有效
		{"example.com", "example.com"},     // 域名条目有效
		{"1.2.3.4", "1.2.3.4"},             // 默认地址本身有效
		{"9.9.9.9", "1.2.3.4"},             // 引用失效 → 回退默认
	}
	for _, c := range cases {
		if got := ResolveServerAddress(srv, c.selected); got != c.want {
			t.Errorf("ResolveServerAddress(%q) = %q, want %q", c.selected, got, c.want)
		}
	}
	// learned_addr 兜底也属于集合
	srv2 := &Server{Address: "1.2.3.4", LearnedAddr: "5.6.7.8"}
	if got := ResolveServerAddress(srv2, "5.6.7.8"); got != "5.6.7.8" {
		t.Errorf("learned fallback = %q, want 5.6.7.8", got)
	}
}

// TestRefreshServerAddresses 验证 session.open 学习合并：访问流 learned 居首、
// NIC 公网地址全部并入去重（不做同族覆盖）、管理员手工条目保留、采不到则缺失。
func TestRefreshServerAddresses(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) *Store {
		st, err := Open(filepath.Join(t.TempDir(), "refresh.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	addressesOf := func(t *testing.T, st *Store, id int64) []string {
		srv, err := st.ServerByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ParseServerAddresses(srv.Addresses)
	}
	equal := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	t.Run("访问流居首并合并NIC双族", func(t *testing.T) {
		st := open(t)
		id, err := st.CreateServer(ctx, ServerDraft{Alias: "a", BootstrapToken: "tok", MachineType: "direct"})
		if err != nil {
			t.Fatal(err)
		}
		srv, _ := st.ServerByID(ctx, id)
		// 访问流学到 1.2.3.4；NIC 上报同族 1.2.3.4 与异族 2400::1 —— 全部保留。
		if err := st.RefreshServerAddresses(ctx, srv, "1.2.3.4", []string{"1.2.3.4", "2400:cb00::1"}); err != nil {
			t.Fatal(err)
		}
		got := addressesOf(t, st, id)
		want := []string{"1.2.3.4", "2400:cb00::1"}
		if !equal(got, want) {
			t.Fatalf("addresses = %v, want %v", got, want)
		}
	})

	t.Run("手工条目保留且默认地址不丢", func(t *testing.T) {
		st := open(t)
		id, err := st.CreateServer(ctx, ServerDraft{Alias: "b", Address: "9.9.9.9", BootstrapToken: "tok", MachineType: "direct"})
		if err != nil {
			t.Fatal(err)
		}
		// 手工再加一个地址，默认仍是 9.9.9.9。
		if err := st.UpdateServerAddresses(ctx, id, []string{"9.9.9.9", "8.8.8.8"}, "9.9.9.9"); err != nil {
			t.Fatal(err)
		}
		// 模拟一次 session.open：learned/nic 已落库（TouchServer），再合并刷新。
		if err := st.TouchServer(ctx, id, "", "", "9.9.9.9", "1.2.3.4", `["1.2.3.4","2400:cb00::1"]`); err != nil {
			t.Fatal(err)
		}
		srv, _ := st.ServerByID(ctx, id)
		if err := st.RefreshServerAddresses(ctx, srv, "1.2.3.4", []string{"1.2.3.4", "2400:cb00::1"}); err != nil {
			t.Fatal(err)
		}
		got := addressesOf(t, st, id)
		want := []string{"1.2.3.4", "2400:cb00::1", "9.9.9.9", "8.8.8.8"}
		if !equal(got, want) {
			t.Fatalf("addresses = %v, want %v", got, want)
		}
		srv, _ = st.ServerByID(ctx, id)
		if srv.Address != "9.9.9.9" || srv.AddressMode != AddressModeManual {
			t.Fatalf("manual 默认地址被改动: address=%q mode=%q", srv.Address, srv.AddressMode)
		}
	})

	t.Run("采不到公网地址则列表缺失", func(t *testing.T) {
		st := open(t)
		id, err := st.CreateServer(ctx, ServerDraft{Alias: "c", BootstrapToken: "tok", MachineType: "nat"})
		if err != nil {
			t.Fatal(err)
		}
		srv, _ := st.ServerByID(ctx, id)
		// NAT：learned 非公网（dispatch 侧过滤后传空），NIC 全私网（过滤后为空）。
		if err := st.RefreshServerAddresses(ctx, srv, "", nil); err != nil {
			t.Fatal(err)
		}
		if got := addressesOf(t, st, id); len(got) != 0 {
			t.Fatalf("addresses = %v, want empty", got)
		}
	})

	t.Run("重复刷新幂等", func(t *testing.T) {
		st := open(t)
		id, err := st.CreateServer(ctx, ServerDraft{Alias: "d", BootstrapToken: "tok", MachineType: "direct"})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			srv, _ := st.ServerByID(ctx, id)
			if err := st.RefreshServerAddresses(ctx, srv, "1.2.3.4", []string{"2400:cb00::1"}); err != nil {
				t.Fatal(err)
			}
		}
		got := addressesOf(t, st, id)
		want := []string{"1.2.3.4", "2400:cb00::1"}
		if !equal(got, want) {
			t.Fatalf("addresses = %v, want %v", got, want)
		}
	})
}
