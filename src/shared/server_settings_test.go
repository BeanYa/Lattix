package shared

import (
	"encoding/json"
	"testing"
)

func TestValidateXrayVersion(t *testing.T) {
	for _, ok := range []string{"", "latest", "v1.8.24", "v0.0.1"} {
		if err := ValidateXrayVersion(ok); err != nil {
			t.Fatalf("ValidateXrayVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"1.8.24", "latest ", "v1.8", "xray-core", "v1.8.24.1"} {
		if err := ValidateXrayVersion(bad); err == nil {
			t.Fatalf("ValidateXrayVersion(%q) = nil, want error", bad)
		}
	}
}

func TestDefaultServerSettings(t *testing.T) {
	settings := DefaultServerSettings()
	if settings.XrayVersion == nil || *settings.XrayVersion != "latest" {
		t.Fatalf("default xray_version = %v, want latest", settings.XrayVersion)
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("default validate: %v", err)
	}
}

func TestServerSettingsDocumentValidate(t *testing.T) {
	good := ServerSettingsDocument{SchemaVersion: ServerSettingsSchemaVersion, Revision: 3, Server: DefaultServerSettings()}
	if err := good.Validate(); err != nil {
		t.Fatalf("good doc validate: %v", err)
	}
	bad := []ServerSettingsDocument{
		{SchemaVersion: 0, Revision: 1, Server: DefaultServerSettings()},
		{SchemaVersion: ServerSettingsSchemaVersion, Revision: 0, Server: DefaultServerSettings()},
		{SchemaVersion: ServerSettingsSchemaVersion, Revision: 1, Server: ServerSettings{}},
	}
	for i, doc := range bad {
		if err := doc.Validate(); err == nil {
			t.Fatalf("bad doc %d validate = nil, want error", i)
		}
	}
}

func TestServerSettingsDocumentJSONRoundTrip(t *testing.T) {
	version := "v1.8.24"
	doc := ServerSettingsDocument{
		SchemaVersion: ServerSettingsSchemaVersion,
		Revision:      5,
		Server:        ServerSettings{XrayVersion: &version},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ServerSettingsDocument
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Revision != 5 || decoded.Server.XrayVersion == nil || *decoded.Server.XrayVersion != "v1.8.24" {
		t.Fatalf("round trip = %+v", decoded)
	}
	// nil 指针 + omitempty：未设置字段序列化后不出现。
	var empty ServerSettings
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("empty settings = %s, want {}", raw)
	}
}
