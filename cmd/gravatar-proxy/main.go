package main

import (
    "context"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "gravatar-proxy/internal/cache"
    "gravatar-proxy/internal/config"
    "gravatar-proxy/internal/log"
    "gravatar-proxy/internal/proxy"
)

func main() {
    log.Info("starting gravatar-proxy")

    cfg, err := config.Load()
    if err != nil {
        log.Error("failed to load config", "error", err)
        os.Exit(1)
    }

    log.Info("loaded configuration",
        "port", cfg.Port,
        "cache_dir", cfg.CacheDir,
        "cache_ttl", cfg.CacheTTL,
        "max_cache_bytes", cfg.MaxCacheBytes,
        "upstream_base", cfg.UpstreamBase,
    )

    c, err := cache.New(cfg.CacheDir, cfg.CacheTTL, cfg.MaxCacheBytes)
    if err != nil {
        log.Error("failed to initialize cache", "error", err)
        os.Exit(1)
    }

    handler, err := proxy.NewHandler(cfg, c)
    if err != nil {
        log.Error("failed to create proxy handler", "error", err)
        os.Exit(1)
    }

    mux := http.NewServeMux()
    mux.Handle("/avatar/", handler)
    mux.HandleFunc("/healthz", proxy.HealthHandler)

    server := &http.Server{
        Addr:         ":" + cfg.Port,
        Handler:      mux,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    go func() {
        log.Info("server listening", "addr", server.Addr)
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error("server error", "error", err)
            os.Exit(1)
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Info("shutting down server")

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if err := server.Shutdown(ctx); err != nil {
        log.Error("server forced to shutdown", "error", err)
        os.Exit(1)
    }

    log.Info("server stopped gracefully")
}
