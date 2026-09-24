package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/httpapi"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
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
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("close data store: %v", err)
		}
	}()
	service := upstream.New(st)
	handler, err := httpapi.New(st, service, webassets.FS())
	if err != nil {
		log.Fatalf("create HTTP server: %v", err)
	}
	cfg := st.Config()
	warnUnauthenticatedAccess(cfg)
	host := stringsOrDefault(os.Getenv("BIND_HOST"), "127.0.0.1")
	address := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
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
		log.Printf("HTTP 监听地址: %s", address)
		base := cfg.PublicBaseURL
		if base == "" {
			accessHost := host
			if host == "0.0.0.0" || host == "::" {
				accessHost = "127.0.0.1"
			}
			base = "http://" + net.JoinHostPort(accessHost, strconv.Itoa(cfg.Port))
		}
		log.Printf("Cline Pass 上游控制台访问地址: %s/", base)
		log.Printf("OpenAI 兼容代理地址: %s/v1", base)
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
		// Long streams outlast the grace period. Closing their connections
		// ends them, and the handlers still record what they served.
		log.Printf("shutdown: %v", err)
		_ = server.Close()
	}
	// The store closes when main returns, so wait for the history records of
	// requests that were cut off above.
	recordCtx, cancelRecord := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRecord()
	if err := handler.Shutdown(recordCtx); err != nil {
		log.Printf("wait for in-flight records: %v", err)
	}
}

func stringsOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// warnUnauthenticatedAccess says out loud when the declared network boundary is
// the only thing between the internet and an API surface without a credential.
// Such a surface trusts a non-loopback peer because the operator declared that
// it only arrives through a host loopback port mapping or a trusted proxy, and
// the remaining check - the Host header - is client-controlled. Silence would
// make "publish the port by mistake" look like a working setup.
func warnUnauthenticatedAccess(cfg model.Config) {
	// Mirrors the request guards: the console falls back to the proxy key, and
	// the model API opens to the proxy key or any issued key.
	var open []string
	if cfg.AdminKey == "" && cfg.ProxyKey == "" {
		open = append(open, "控制台（含读取账号密钥的完整管理权限）")
	}
	if cfg.ProxyKey == "" && len(cfg.ProxyKeys) == 0 {
		open = append(open, "模型接口")
	}
	if len(open) == 0 {
		return
	}
	surfaces := strings.Join(open, "和")
	if cfg.TrustLocalPortForward {
		log.Printf("[警告] %s未设置密钥，但已开启 TRUST_LOCAL_PORT_FORWARD：任何能连上该端口的来源，只要把 Host 头写成 127.0.0.1 或 localhost，就会被当作本机访问。请确认端口只映射到宿主机回环地址，或设置密钥（控制台：ADMIN_KEY 或 PROXY_KEY；模型接口：PROXY_KEY 或签发客户端密钥）。（compose 默认把端口绑定在 127.0.0.1:3123，属于安全组合，这种情况可忽略本警告；一旦改动 ports 绑定或改用域名访问，就必须设置密钥。）", surfaces)
	}
	if len(cfg.TrustedProxies) > 0 {
		log.Printf("[警告] %s未设置密钥，但已信任反向代理 %s：来自这些地址的 X-Forwarded-For / X-Real-IP 会被采信。请确认这些代理确实无法被绕过，且不会把任意客户端声明成本机。", surfaces, strings.Join(cfg.TrustedProxies, ","))
	}
}
