package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/cdncatalog"
)

func catalogProbeFixtureDoc() *cdncatalog.Document {
	return &cdncatalog.Document{
		Version: cdncatalog.SchemaVersion,
		Provinces: []cdncatalog.Province{{
			Name: "广东",
			Carriers: cdncatalog.CarrierEndpoints{
				Telecom: cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-ct.ip.zstaticcdn.com", Port: 80}},
				Unicom:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-cu.ip.zstaticcdn.com", Port: 80}},
				Mobile:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-cm.ip.zstaticcdn.com", Port: 80}},
			},
		}, {
			Name: "云南",
			Carriers: cdncatalog.CarrierEndpoints{
				Telecom: cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "yn-ct.ip.zstaticcdn.com", Port: 80}},
				Unicom:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "yn-cu.ip.zstaticcdn.com", Port: 80}},
				Mobile:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "yn-cm.ip.zstaticcdn.com", Port: 80}},
			},
		}},
		Cities: []cdncatalog.CityEndpoint{
			{Province: "广东", City: "深圳", Carrier: "电信", CarrierCode: "ct", Host: "sz-ct.ip.zstaticcdn.com", Port: 443},
			{Province: "广东", City: "深圳", Carrier: "联通", CarrierCode: "cu", Host: "sz-cu.ip.zstaticcdn.com", Port: 443},
			{Province: "广东", City: "深圳", Carrier: "移动", CarrierCode: "cm", Host: "sz-cm.ip.zstaticcdn.com", Port: 443},
			{Province: "云南", City: "昆明", Carrier: "电信", CarrierCode: "ct", Host: "km-ct.ip.zstaticcdn.com", Port: 443},
		},
	}
}

func TestCatalogProbeRegionDefaults(t *testing.T) {
	// 未设置/空/损坏 JSON 均回默认省份。
	for _, raw := range []string{"", "   ", "not-json"} {
		region := CatalogProbeRegionFromRaw(raw)
		if region.Province != DefaultCatalogProbeProvince || region.City != "" {
			t.Fatalf("raw %q: got %+v", raw, region)
		}
	}
	region := CatalogProbeRegionFromRaw(`{"province":" 云南 ","city":" 昆明 "}`)
	if region.Province != "云南" || region.City != "昆明" {
		t.Fatalf("trimmed region mismatch: %+v", region)
	}
}

func TestCatalogProbeTargetsForRegion(t *testing.T) {
	doc := catalogProbeFixtureDoc()

	provinceTargets := CatalogProbeTargetsForRegion(doc, CatalogProbeRegion{Province: "广东"})
	if len(provinceTargets) != 3 {
		t.Fatalf("province targets = %d, want 3", len(provinceTargets))
	}
	byCarrier := map[string]CatalogProbeTarget{}
	for _, target := range provinceTargets {
		byCarrier[target.Carrier] = target
	}
	telecom := byCarrier["telecom"]
	if telecom.Label != "广东电信" || telecom.Address != "gd-ct.ip.zstaticcdn.com" || telecom.Port != 80 {
		t.Fatalf("telecom target mismatch: %+v", telecom)
	}
	if telecom.Key != "catalog:广东:*:telecom" {
		t.Fatalf("telecom key mismatch: %s", telecom.Key)
	}

	cityTargets := CatalogProbeTargetsForRegion(doc, CatalogProbeRegion{Province: "广东", City: "深圳"})
	if len(cityTargets) != 3 {
		t.Fatalf("city targets = %d, want 3", len(cityTargets))
	}
	if cityTargets[0].Key != "catalog:广东:深圳:telecom" || cityTargets[0].Label != "深圳电信" ||
		cityTargets[0].Address != "sz-ct.ip.zstaticcdn.com" || cityTargets[0].Port != 443 {
		t.Fatalf("city telecom target mismatch: %+v", cityTargets[0])
	}

	// 城市不属于该省 / 省份不在目录 → 空集。
	if got := CatalogProbeTargetsForRegion(doc, CatalogProbeRegion{Province: "云南", City: "深圳"}); len(got) != 0 {
		t.Fatalf("cross-province city targets = %d, want 0", len(got))
	}
	if got := CatalogProbeTargetsForRegion(doc, CatalogProbeRegion{Province: "北京"}); len(got) != 0 {
		t.Fatalf("unknown province targets = %d, want 0", len(got))
	}
}

func TestCatalogProbeTargetsFromStoreSettings(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 目录未抓取 → 空集非错误。
	targets, region, err := st.CatalogProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if region.Province != DefaultCatalogProbeProvince || len(targets) != 0 {
		t.Fatalf("empty catalog: targets=%d region=%+v", len(targets), region)
	}
	if err := st.SetSetting(ctx, SettingCatalogProbeRegion, `{"province":"云南"}`); err != nil {
		t.Fatal(err)
	}
	// 目录版本不符 → 空集。
	if err := st.SetSetting(ctx, SettingCDNNodeCatalog, `{"version":0}`); err != nil {
		t.Fatal(err)
	}
	targets, _, err = st.CatalogProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("stale catalog targets = %d, want 0", len(targets))
	}
	// 有效目录 + 云南设置 → 3 个省份目标。
	if err := st.SetSetting(ctx, SettingCDNNodeCatalog, mustJSONDoc(t, catalogProbeFixtureDoc())); err != nil {
		t.Fatal(err)
	}
	targets, region, err = st.CatalogProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if region.Province != "云南" || len(targets) != 3 {
		t.Fatalf("region=%+v targets=%d, want 3", region, len(targets))
	}
}

func mustJSONDoc(t *testing.T, doc *cdncatalog.Document) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSaveServerCatalogProbesUpsertsLatestAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 12.5
	if err := st.SaveServerCatalogProbes(ctx, 1, []CatalogProbeSample{
		{Key: "catalog:广东:*:telecom", Kind: "tcp", LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// 同键覆盖最新值、保留历史；失败样本（nil）照常落库。
	if err := st.SaveServerCatalogProbes(ctx, 1, []CatalogProbeSample{
		{Key: "catalog:广东:*:telecom", Kind: "tcp"},
		{Key: "catalog:广东:*:unicom", Kind: "tcp", LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshServerCatalogProbes(ctx, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 2 {
		t.Fatalf("fresh rows = %d, want 2", len(fresh))
	}
	byKey := map[string]CatalogProbeRow{}
	for _, row := range fresh {
		byKey[row.TargetKey] = row
	}
	if row := byKey["catalog:广东:*:telecom"]; row.LatencyMS != nil {
		t.Fatalf("telecom latest should be failed sample: %+v", row)
	}
	if row := byKey["catalog:广东:*:unicom"]; row.LatencyMS == nil || *row.LatencyMS != 12.5 {
		t.Fatalf("unicom latest mismatch: %+v", row)
	}
	var historyCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM server_catalog_probe_history`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 3 {
		t.Fatalf("history rows = %d, want 3", historyCount)
	}
}

func TestCatalogProbeLossStatsWindow(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 8.0
	now := time.Now().UTC()
	if err := st.SaveServerCatalogProbes(ctx, 1, []CatalogProbeSample{
		{Key: "catalog:广东:*:telecom", Kind: "tcp", LatencyMS: &latency},
		{Key: "catalog:广东:*:unicom", Kind: "tcp", LatencyMS: &latency},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveServerCatalogProbes(ctx, 1, []CatalogProbeSample{
		{Key: "catalog:广东:*:telecom", Kind: "tcp"},
		{Key: "catalog:广东:*:telecom", Kind: "tcp"},
	}, now); err != nil {
		t.Fatal(err)
	}
	lossRows, err := st.CatalogProbeLossStats(ctx, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]CatalogProbeLossRow{}
	for _, row := range lossRows {
		byKey[row.TargetKey] = row
	}
	telecom := byKey["catalog:广东:*:telecom"]
	if telecom.Samples != 3 || telecom.Failed != 2 {
		t.Fatalf("telecom loss mismatch: %+v", telecom)
	}
	if loss := telecom.LossPercent(); loss == nil || *loss < 66.66 || *loss > 66.67 {
		t.Fatalf("telecom loss = %v", loss)
	}
	unicom := byKey["catalog:广东:*:unicom"]
	if unicom.Samples != 1 || unicom.Failed != 0 {
		t.Fatalf("unicom loss mismatch: %+v", unicom)
	}
}

func TestDeleteExpiredServerCatalogProbeHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 8.0
	if err := st.SaveServerCatalogProbes(ctx, 1, []CatalogProbeSample{
		{Key: "catalog:广东:*:telecom", Kind: "tcp", LatencyMS: &latency},
	}, time.Now().UTC().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.DeleteExpiredServerCatalogProbeHistory(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := st.DeleteExpiredServerCatalogProbeHistory(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
}
