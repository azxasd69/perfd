// Команда perfd: сервис портала замеров PerfTest.
// Режимы: сервер (по умолчанию), -watch (файловый приём), -retention (свёртка),
// -export (снимок по требованию), -check (проверка восстановленной базы).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"perfd/internal/alerts"
	"perfd/internal/config"
	"perfd/internal/dnssrv"
	"perfd/internal/maildrop"
	"perfd/internal/model"
	"perfd/internal/obs"
	"perfd/internal/store"
	"perfd/internal/watch"
	"perfd/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("perfd остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		modeWatch     = flag.Bool("watch", false, "файловый приёмник: обход in/ и подтверждения в out/")
		modeRetention = flag.Bool("retention", false, "свёртка старого детального ряда и удаление по сроку")
		modeExport    = flag.String("export", "", "снимок базы в указанный файл (.gz) с манифестом")
		modeCheck     = flag.Bool("check", false, "открыть базу и напечатать счётчики")
		addr          = flag.String("addr", "", "переопределить адрес прослушивания")
		dbPath        = flag.String("db", "", "переопределить путь к базе")
		inDir         = flag.String("in", "/var/lib/perf/in", "каталог входящих файлов")
		outDir        = flag.String("out", "/var/lib/perf/out", "каталог подтверждений")
		archiveDir    = flag.String("archive", "/var/lib/perf/archive", "архив обработанных файлов")
		channel       = flag.String("channel", "webdav", "имя канала для файлового приёма: webdav|ftps|sftp")
		version       = flag.Bool("version", false, "напечатать версию и выйти")
		modeMail      = flag.Bool("mail", false, "почтовый приёмник: Maildir perfing -> конвейер, ack в perfack")
		modeDNS       = flag.Bool("dns", false, "DNS-приёмник TXT-конвертов по TCP")
		modeAlerts    = flag.Bool("alerts", false, "оповещения: почта, webhook, метрики")
		maildir       = flag.String("maildir", "", "корень Maildir (по умолчанию PERF_MAILDIR)")
		listenDNS     = flag.String("listen", "", "адрес DNS-приёмника (по умолчанию PERF_DNS_LISTEN)")
		zoneDNS       = flag.String("zone", "", "зона DNS (по умолчанию PERF_DNS_ZONE)")
		metricsAddr   = flag.String("metrics-addr", "127.0.0.1:9091", "адрес метрик для режима -alerts")
	)
	flag.Parse()

	if *version {
		fmt.Println(web.Version)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	log := obs.NewLogger(cfg.LogLevel)

	// Проверка восстановленного снимка не должна менять файл: открываем только на чтение.
	if *modeCheck {
		ro, err := store.OpenReadOnly(cfg.DBPath)
		if err != nil {
			return err
		}
		defer func() { _ = ro.Close() }()
		report, err := ro.Check(context.Background())
		if err != nil {
			return err
		}
		fmt.Println(report)
		return nil
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	err = st.Migrate(migrateCtx)
	cancelMigrate()
	if err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	switch {
	case *modeRetention:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := st.Retention(ctx, cfg.RetentionMonths, cfg.RetentionLogDays)
		if err != nil {
			return err
		}
		log.Info("обслуживание выполнено",
			"months", res.RolledMonths, "rows", res.DeletedRows, "log", res.LogDeleted, "vacuum", res.Vacuumed)
		fmt.Printf("свёрнуто месяцев: %d, удалено строк истории: %d, журнал: %d\n",
			res.RolledMonths, res.DeletedRows, res.LogDeleted)
		return nil

	case *modeExport != "":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		manifest, n, err := st.Export(ctx, *modeExport)
		if err != nil {
			return err
		}
		fmt.Printf("снимок: %s (%d байт), манифест: %s\n", *modeExport, n, manifest)
		return nil

	case *modeWatch:
		ctx, cancel := signalContext()
		defer cancel()
		log.Info("perfd -watch", "in", *inDir, "channel", *channel, "version", web.Version)
		return watch.Run(ctx, st, log, watch.Options{
			InDir: *inDir, OutDir: *outDir, ArchiveDir: *archiveDir,
			Channel: *channel, Formula: cfg.Formula,
		})

	case *modeMail:
		ctx, cancel := signalContext()
		defer cancel()
		root := *maildir
		if root == "" {
			root = cfg.Maildir
		}
		log.Info("perfd -mail", "maildir", root, "version", web.Version)
		return maildrop.Run(ctx, st, log, maildrop.Options{MailRoot: root, Formula: cfg.Formula})

	case *modeDNS:
		ctx, cancel := signalContext()
		defer cancel()
		addr := *listenDNS
		if addr == "" {
			addr = cfg.DNSListen
		}
		zone := *zoneDNS
		if zone == "" {
			zone = cfg.DNSZone
		}
		log.Info("perfd -dns", "listen", addr, "zone", zone, "version", web.Version)
		return dnssrv.New(st, log, dnssrv.Options{Listen: addr, Zone: zone, Formula: cfg.Formula}).Run(ctx)

	case *modeAlerts:
		ctx, cancel := signalContext()
		defer cancel()
		log.Info("perfd -alerts", "metrics", *metricsAddr, "version", web.Version)
		return alerts.Run(ctx, st, log, alerts.Options{
			Webhook: cfg.AlertWebhook, SMTPHost: cfg.SMTPHost, SMTPPort: cfg.SMTPPort,
			SMTPUser: cfg.SMTPUser, SMTPPass: cfg.SMTPPass, From: cfg.AlertFrom, To: cfg.AlertEmail,
			Cooldown: cfg.AlertCooldown, LogPath: "/var/log/perf/alerts.log", Channels: cfg.Channels,
		})
	}

	metrics := obs.NewMetrics()
	srv, err := web.New(st, log, metrics, web.Options{
		Base:              cfg.BasePath,
		MaxBody:           cfg.MaxBody,
		Schema:            cfg.Schema,
		Formula:           cfg.Formula,
		DefaultBase:       cfg.DefaultBase,
		IngestTokenSHA256: cfg.TokenSHA256,
		Channels:          cfg.Channels,
		StatusHost:        cfg.StatusHost,
		PurgeRoots:        purgeRoots(*inDir, *outDir, *archiveDir, &cfg),
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           obs.Middleware(log, metrics, srv.Handler()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if cfg.TokenSHA256 == "" {
		log.Warn("PERF_INGEST_TOKEN_SHA256 не задан: приём будет отвергать все конверты (401)")
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("perfd стартует",
			"addr", cfg.Addr, "db", cfg.DBPath, "base_path", cfg.BasePath,
			"schema", cfg.Schema, "formula", cfg.Formula, "channels", cfg.Channels, "version", web.Version)
		errCh <- httpSrv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case sig := <-sigCh:
		log.Info("perfd останавливается", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// purgeRoots — что чистит кнопка «Очистить данные»: каталоги каналов обмена
// (удаляется содержимое, сами каталоги остаются — иначе файловый приёмник
// и WebDAV потеряют точки монтирования) плюс снимки базы и почтовые ящики приёма.
//
// СОЗНАТЕЛЬНО НЕ ЧИСТИТСЯ: /var/lib/perf/sftp — это chroot канала SFTP
// с .ssh/authorized_keys; удаление содержимого отрезало бы канал и сам доступ.
func purgeRoots(inDir, outDir, archiveDir string, cfg *config.Config) []string {
	dataDir := filepath.Dir(inDir)
	roots := []string{
		inDir, outDir, archiveDir,
		filepath.Join(dataDir, "quarantine"),
		filepath.Join(dataDir, "tmp"),
		filepath.Join(dataDir, "perf-snap.db.gz"),
		filepath.Join(dataDir, "perf-snap.db.gz.manifest.json"),
	}
	if cfg.Maildir != "" {
		roots = append(roots,
			filepath.Join(cfg.Maildir, "perfing"),
			filepath.Join(cfg.Maildir, "perfack"))
	}
	return roots
}

func signalContext() (context.Context, context.CancelFunc) {	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

var _ = model.Schema
