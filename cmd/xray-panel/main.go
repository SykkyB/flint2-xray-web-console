package main

import (
	"context"
	"errors"
	"flag"
	"log"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"flint2-xray-web-console/internal/config"
	panelhttp "flint2-xray-web-console/internal/http"
	"flint2-xray-web-console/internal/service"
	"flint2-xray-web-console/internal/store"
	"flint2-xray-web-console/internal/xray"
)

func main() {
	configPath := flag.String("config", "/etc/xray-panel/panel.yaml", "path to panel config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := panelhttp.CheckLANBind(cfg.Listen); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}

	mgr := &service.Manager{
		InitScript: cfg.XrayInit,
		XrayBin:    cfg.XrayBin,
		ConfigPath: cfg.XrayConfig,
		Timeout:    15 * time.Second,
	}
	keys := &xray.KeyTool{
		XrayBin: cfg.XrayBin,
		Timeout: 5 * time.Second,
	}
	srv := &panelhttp.Server{
		Cfg:             cfg,
		Service:         mgr,
		Keys:            keys,
		Disabled:        store.New(cfg.DisabledStore),
		ConfPath:        cfg.XrayConfig,
		PanelConfigPath: *configPath,
	}

	httpSrv := &nethttp.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("xray-panel listening on %s", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
