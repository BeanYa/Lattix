package shared

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const credentialPrefix = "ltx1"

type Credential struct {
	PanelInstanceID string
	Epoch           int64
	Secret          string
}

func NewPanelInstanceID() (string, error) {
	value, err := randomHexBytes(16)
	if err != nil {
		return "", err
	}
	return "p_" + value, nil
}

func NewCredential(panelInstanceID string, epoch int64) (string, error) {
	if panelInstanceID == "" || strings.Contains(panelInstanceID, ".") {
		return "", errors.New("invalid panel instance id")
	}
	if epoch < 1 {
		return "", errors.New("credential epoch must be at least 1")
	}
	secret, err := randomHexBytes(32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s.%d.%s", credentialPrefix, panelInstanceID, epoch, secret), nil
}

func ParseCredential(value string) (Credential, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != credentialPrefix || parts[1] == "" {
		return Credential{}, errors.New("invalid credential format")
	}
	epoch, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || epoch < 1 {
		return Credential{}, errors.New("invalid credential epoch")
	}
	secret, err := hex.DecodeString(parts[3])
	if err != nil || len(secret) != 32 {
		return Credential{}, errors.New("invalid credential secret")
	}
	return Credential{PanelInstanceID: parts[1], Epoch: epoch, Secret: parts[3]}, nil
}

func randomHexBytes(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
