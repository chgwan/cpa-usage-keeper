package app

import (
	"net/http"
	"time"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/logging"
	"github.com/sirupsen/logrus"
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpIdleTimeout       = 60 * time.Second
	httpMaxHeaderBytes    = 64 << 10
	// 读期限覆盖 header 加 body，含处理结束后服务端排空未发完 body 的过程；没有它，匿名连接只发一个字节就能长期占用连接。
	httpReadTimeout = 30 * time.Second
	// 写期限要宽到容得下最慢的管理端下载，只用来挡住读得极慢的客户端。
	httpWriteTimeout = 180 * time.Second
)

func NewHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           handler,
		ErrorLog:          logging.NewStandardLogger(logrus.ErrorLevel),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}
