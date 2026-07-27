// coinsphere Go 后端:单二进制,同一进程内运行 HTTP API 与工作流运行时。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"coinsphere/backend/internal/api"
	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/internal/service"
)

// main 是整个程序的入口:Go 程序从 main 包里的 main() 函数开始执行。
// 启动顺序固定:加载配置 → 打开数据库 → 建表种子 → 构建 App → 启动运行时 → 起 HTTP → 等信号优雅关机。
func main() {
	// 第 1 步:解析命令行参数。flag 是标准库,用来读 -config 这类启动参数。
	// := 是"短声明":自动推断类型、只能在函数内部用。flag.String 返回的是 *string 指针,
	// 真正的值要等 flag.Parse() 解析完命令行后,再用 *configPath 取出来(见下一段)。
	configPath := flag.String("config", "", "配置文件路径(默认 config.yml,可用 COINSPHERE_CONFIG_PATH 覆盖)")
	flag.Parse()

	// 第 2 步:加载配置。Go 函数常常返回多个值,这里同时接住配置 cfg 和错误 err。
	// *configPath 前面的 * 是"顺着指针取值",把上面得到的 *string 还原成字符串。
	// Go 没有 try/except,靠"返回 error + if err != nil"判断成败;出错就 log.Fatalf 打日志并退出进程。
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 第 3 步:按配置打开数据库(内部会按 driver 选方言并自动建表,见 db 包)。
	gdb, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	// 第 4 步:准备密码哈希器,并写入种子数据(内置管理员、菜单与权限等)。
	hasher := security.NewPasswordHasher(cfg.Auth.PasswordIterations)
	if err := db.Seed(gdb, hasher); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	log.Printf("database ready: driver=%s", cfg.Database.Driver)

	// 第 5 步:构建 App(承载工作流运行时)。workerID 用"主机名:进程号"标识当前进程实例。
	// hostname, _ := ... 里的 _ 是"下划线丢弃":这里不关心 os.Hostname 的错误,直接扔掉。
	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s:%d", hostname, os.Getpid())
	app, err := service.NewApp(gdb, cfg, workerID)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}
	// 启动后台运行时:内部会开一批 goroutine(调度、执行循环等)在本进程内并发运行。
	app.StartRuntime()

	// 第 6 步:装配 HTTP 服务。filepath.Join 按操作系统分隔符拼路径(Windows 用 \、Linux 用 /),比手写字符串安全。
	executable, _ := os.Executable()
	baseDir := filepath.Dir(executable)
	staticDir := filepath.Join(baseDir, "volumes", "static")
	uploadsDir := filepath.Join(baseDir, "volumes", "uploads")
	mux := api.NewServer(app, staticDir, uploadsDir)

	// &http.Server{...} 用 & 取地址,得到一个指向 http.Server 的指针;Handler 就是上面建好的路由 mux。
	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: mux,
	}
	// 第 7 步:起 HTTP 服务。go func(){...}() 开一条 goroutine 让服务在后台监听,
	// 主流程才能继续往下走去等退出信号(否则 ListenAndServe 会一直阻塞在这里)。
	// 正常关机时 ListenAndServe 返回 http.ErrServerClosed,这一种不算错误。
	go func() {
		log.Printf("http server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// 第 8 步:优雅关机。channel 是 goroutine 之间传信号的管道(见 GO入门笔记『并发』)。
	// make(chan os.Signal, 1) 建一个容量 1 的信号 channel;signal.Notify 把 Ctrl+C / SIGTERM 转发进来。
	// <-quit 从 channel 接收,会一直阻塞,直到收到信号才继续往下执行。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Printf("shutting down...")

	// 给关机一个 30 秒超时:context 用来传递"到点该停手了/超时了"的信号。
	// defer cancel() 登记收尾——不管函数怎么返回,退出前一定会调用 cancel() 释放这个 context。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 先让 HTTP 停止接收新请求(等在途请求处理完或到超时),再停后台运行时。_ = 表示忽略返回的错误。
	_ = server.Shutdown(ctx)
	app.StopRuntime()
	log.Printf("bye")
}
