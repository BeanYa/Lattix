package alert

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type echoURLRequester struct{}

func (echoURLRequester) PostJSON(_ context.Context, url string, _ any) error {
	return errors.New("request failed: " + url)
}

func TestSendTelegramRedactsBotToken(t *testing.T) {
	n := &Notifier{requester: echoURLRequester{}}
	err := n.sendTelegram(context.Background(), "123456:secret-token", "chat", "test")
	if err == nil {
		t.Fatal("sendTelegram unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "123456:secret-token") {
		t.Fatalf("telegram token leaked in error: %s", err)
	}
}
