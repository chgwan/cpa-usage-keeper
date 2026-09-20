package test

import (
	"net/http"
	"testing"
	"time"

	keeperapp "cpa-usage-keeper/internal/app"
	"cpa-usage-keeper/internal/config"
)

func TestHTTPServerBoundsConnectionSetupAndRequestReads(t *testing.T) {
	server := keeperapp.NewHTTPServer(config.Config{AppHost: "127.0.0.1", AppPort: "8080"}, http.NotFoundHandler())

	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected five second read-header timeout, got %s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("expected sixty second idle timeout, got %s", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("expected 64 KiB header limit, got %d", server.MaxHeaderBytes)
	}
	// 没有读超时时，匿名连接可以声明 body 却只发一个字节，让服务端在排空 body 时无限期挂住。
	if server.ReadTimeout != 30*time.Second {
		t.Fatalf("expected thirty second request read timeout, got %s", server.ReadTimeout)
	}
	if server.WriteTimeout != 180*time.Second {
		t.Fatalf("expected a write deadline that bounds slow readers, got %s", server.WriteTimeout)
	}
}
