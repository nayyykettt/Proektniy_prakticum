// Команда teampulse — сервис автоматической оценки эффективности команд разработки.
//
//	teampulse serve                          — веб-интерфейс, API и планировщик (по умолчанию)
//	teampulse sync   [-project ID]           — синхронизировать один или все проекты
//	teampulse healthcheck                    — проверка живости запущенного сервера (для Docker)
//	teampulse report -project ID [-from ГГГГ-ММ-ДД] [-to ГГГГ-ММ-ДД] [-format md|html|csv|json] [-out файл]
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
	"strings"
	"syscall"
	"time"

	"teampulse/internal/app"
	"teampulse/internal/config"
	"teampulse/internal/report"
	"teampulse/internal/scheduler"
	"teampulse/internal/secure"
	"teampulse/internal/server"
	"teampulse/internal/storage"
	"teampulse/internal/zerocracy"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	if cmd == "healthcheck" { // для HEALTHCHECK в distroless-образе, где нет curl
		os.Exit(healthcheck())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, closeFn, err := setup(ctx, log)
	if err != nil {
		log.Error("запуск", "err", err)
		os.Exit(1)
	}
	defer closeFn()

	switch cmd {
	case "serve":
		err = serve(ctx, a)
	case "sync":
		err = syncCmd(ctx, a, args)
	case "report":
		err = reportCmd(ctx, a, args)
	default:
		err = fmt.Errorf("неизвестная команда %q (serve | sync | report)", cmd)
	}
	if err != nil {
		log.Error(cmd, "err", err)
		os.Exit(1)
	}
}

func setup(ctx context.Context, log *slog.Logger) (*app.App, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	box, err := secure.NewBox(cfg.AppSecret)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	pol, err := zerocracy.LoadPolicy(cfg.PolicyPath)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return app.New(cfg, store, box, pol, log), func() { store.Close() }, nil
}

func serve(ctx context.Context, a *app.App) error {
	if err := a.Store.ResetRunningSyncs(ctx); err != nil {
		return err
	}
	handler, err := server.New(a, server.NewAuth(a))
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: a.Cfg.Addr, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
	go scheduler.Run(ctx, a, time.Minute)

	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("TeamPulse запущен", "addr", a.Cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		a.Log.Info("остановка…")
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
		a.Wait()
	}
	return nil
}

func syncCmd(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	id := fs.Int64("project", 0, "ID проекта (0 — все проекты)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := []int64{*id}
	if *id == 0 {
		ps, err := a.Store.ListProjects(ctx)
		if err != nil {
			return err
		}
		ids = ids[:0]
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
	}
	var failed int
	for _, pid := range ids {
		res, err := a.Sync(ctx, pid)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "проект %d: ошибка: %v\n", pid, err)
			continue
		}
		fmt.Printf("проект %d: %s, новых коммитов %d, MR %d, задач %d (%s)\n",
			pid, res.Status, res.NewCommits, res.MergeRequests, res.Issues, res.Duration)
		for _, w := range res.Warnings {
			fmt.Printf("  ! %s\n", w)
		}
	}
	if failed > 0 {
		return fmt.Errorf("не удалось синхронизировать проектов: %d", failed)
	}
	return nil
}

func reportCmd(ctx context.Context, a *app.App, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	id := fs.Int64("project", 0, "ID проекта")
	from := fs.String("from", "", "начало периода ГГГГ-ММ-ДД (по умолчанию — 30 дней назад)")
	to := fs.String("to", "", "конец периода ГГГГ-ММ-ДД включительно")
	team := fs.Int64("team", 0, "ID команды")
	format := fs.String("format", "md", "формат: md | html | csv | json")
	out := fs.String("out", "", "файл (по умолчанию — stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return errors.New("укажите -project")
	}
	period, err := app.ParsePeriod(*from, *to, time.Now())
	if err != nil {
		return err
	}
	rep, err := a.GenerateReport(ctx, *id, period, *team, "manual")
	if err != nil {
		return err
	}
	d, _, err := a.LoadReport(ctx, rep.ID)
	if err != nil {
		return err
	}
	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return report.Render(w, d, *format)
}

func healthcheck() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
