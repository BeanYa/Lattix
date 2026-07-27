package shared

import "testing"

func TestCredentialRoundTrip(t *testing.T) {
	panelID, err := NewPanelInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewCredential(panelID, 7)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := ParseCredential(token)
	if err != nil {
		t.Fatal(err)
	}
	if credential.PanelInstanceID != panelID || credential.Epoch != 7 {
		t.Fatalf("credential = %#v", credential)
	}
}
