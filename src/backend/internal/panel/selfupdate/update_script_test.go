package selfupdate

import "testing"

func TestPanelCLIManaged(t *testing.T) {
	t.Setenv("LATTIX_DEPLOY_MODE", "docker")
	if panelCLIManaged() {
		t.Fatal("Docker deployments must not manage the host latx command")
	}

	t.Setenv("LATTIX_DEPLOY_MODE", "native")
	if !panelCLIManaged() {
		t.Fatal("native deployments must update latx with the panel")
	}
}
