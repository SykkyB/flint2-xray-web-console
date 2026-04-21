package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"flint2-xray-web-console/internal/config"
)

func main() {
	configPath := flag.String("config", "/etc/xray-panel/panel.yaml", "path to panel config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	fmt.Fprintf(os.Stdout, "xray-panel: loaded config from %s\n", *configPath)
	fmt.Fprintf(os.Stdout, "  listen:         %s\n", cfg.Listen)
	fmt.Fprintf(os.Stdout, "  server_address: %s\n", cfg.ServerAddress)
	fmt.Fprintf(os.Stdout, "  xray_config:    %s\n", cfg.XrayConfig)
	fmt.Fprintf(os.Stdout, "  xray_bin:       %s\n", cfg.XrayBin)
	fmt.Fprintf(os.Stdout, "  xray_init:      %s\n", cfg.XrayInit)
	fmt.Fprintf(os.Stdout, "  stats_api:      %q\n", cfg.StatsAPI)
	fmt.Fprintf(os.Stdout, "  disabled_store: %s\n", cfg.DisabledStore)

	log.Println("HTTP server not yet implemented; exiting.")
}
