package app

import (
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
)

func Main() {
	mode := "all"
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "all", "api", "worker":
			mode = os.Args[1]
		}
	}
	apiMode, workerMode := mode == "all" || mode == "api", mode == "all" || mode == "worker"
	loadSecret()
	if err := initDB(); err != nil {
		log.Fatal("数据库初始化失败: ", err)
	}
	initAdmin()
	loadWxConfig()
	if !wxEnabled() && !wxMock() {
		slog.Warn("微信支付未启用：缺少平台证书或支付凭据，Native 下单不会创建订单")
	}
	if apiMode {
		// Platform certificates are needed by the API callback verifier. Keep
		// refresh ownership in the API process when running split services.
		go startPlatformCertRefresher()
	}
	if workerMode {
		go startReconciler()
		startWorkerHeartbeat()
		go startWorkers(3)
	}
	if !apiMode {
		slog.Info("M-brand worker mode", "mode", mode)
		select {}
	}
	cfg := platform.LoadConfig()
	port := cfg.Port
	slog.Info("M-brand listening", "port", port, "base", basePath, "mode", mode)
	fmt.Println("M-brand → http://127.0.0.1:" + port + basePath + "/")
	if err := buildRouter().Run("127.0.0.1:" + port); err != nil {
		log.Fatal(err)
	}
}
