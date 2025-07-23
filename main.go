// C:/workspace/go/Air-Simulator-Go/main.go
package main

import (
	// 导入您新创建的 environment 包
	"Air-Simulator/environment"
	"time"

	// 导入由 .proto 文件生成的 protos 包
	"Air-Simulator/protos"
	"google.golang.org/grpc"
	"log"
	"math/rand"
	"net"
)

// 定义 gRPC 服务器监听的端口
const grpcPort = ":50051"

func main() {
	// (可选) 设置随机数种子，以便在需要时可以复现仿真结果
	// 在正式训练时，通常使用变化的种子以增加随机性
	rand.Seed(time.Now().UnixNano())

	// 1. 在指定端口上启动TCP监听
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		log.Fatalf("错误：无法在端口 %s 上启动监听: %v", grpcPort, err)
	}

	// 2. 创建一个新的 gRPC 服务器实例
	grpcServer := grpc.NewServer()

	// 3. 创建您的环境服务器实例
	//    NewServer() 函数会负责初始化整个仿真世界
	rlServer := environment.NewServer(environment.Config{
		EnableDualChannel: false,
	})

	// 4. 将您的环境服务注册到 gRPC 服务器上
	//    这样 gRPC 服务器才知道如何将请求转发给您的 Step 和 Reset 方法
	protos.RegisterRLEnvironmentServer(grpcServer, rlServer)

	log.Printf("✅ gRPC 强化学习环境服务器已启动，正在监听端口 %s", grpcPort)

	// 5. 启动 gRPC 服务器并开始接受来自Python客户端的连接
	//    这是一个阻塞操作，程序会一直运行在这里，直到被手动停止
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("错误：gRPC 服务器启动失败: %v", err)
	}
}
