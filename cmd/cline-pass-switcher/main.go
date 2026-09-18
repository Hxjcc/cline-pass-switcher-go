package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/httpapi"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/webassets"
)

func main() {
	dataDir := stringsOrDefault(os.Getenv("DATA_DIR"), ".")
	st, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("open data store: %v", err)
	}
	service := upstream.New(st)
	handler, err := httpapi.New(st, service, webassets.FS())
	if err != nil {
		log.Fatalf("create HTTP server: %v", err)
	}
	cfg := st.Config()
	host := stringsOrDefault(os.Getenv("BIND_HOST"), "127.0.0.1")
	address := host + ":" + strconv.Itoa(cfg.Port)
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if !st.IsConfigured() {
		log.Print("[提示] 尚未配置上游 API Key：打开控制台“账号管理”添加账号并保存即可；服务已启动。")
	}
	go func() {
		log.Printf("Cline Pass 上游控制台: http://127.0.0.1:%d/", cfg.Port)
		log.Printf("OpenAI 兼容代理地址: http://127.0.0.1:%d/v1", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func stringsOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
