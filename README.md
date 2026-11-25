# Research Knowledge Agent

Research Knowledge Agent（RK-Agent）是面向科研团队的 Agent 系统。Go 服务接收科研任务，由 Eino Runtime 调用模型分析任务、选择工具并汇总结果。论文知识库是 Agent 的一个工具，后续与 Web Search、MCP 工具共同提供信息检索和任务执行能力。

## 开发环境与部署硬件

### Mac 开发环境

当前开发机使用 macOS 26.5.1（ARM64）和 Docker Compose 5.5.1。在 Mac 上编写和调试代码，通过 Compose 运行 Agent、Ollama 和 Qdrant，验证服务间的调用。Go 程序在 `golang:1.26.11-alpine` 容器内执行，模块声明的 Go 版本是 `1.24.0`。

当前验证配置使用 Eino `v0.9.23`、Ollama 聊天模型 `qwen3:4b`、向量模型 `qwen3-embedding:0.6b` 和 Qdrant `v1.17.1`。Mac 上的默认 Compose 配置使用 Ollama CPU 镜像。

### Linux 推理与部署服务器

以下是项目设计文档记录的现有服务器硬件，用于后续本地模型推理和部署。

| 硬件 | 配置 |
| --- | --- |
| GPU | AMD Radeon RX 9070 XT，16 GB 显存 |
| CPU | Intel Xeon E5-2696 v4 × 2 |
| 内存 | 64 GB DDR4-2133 RECC |
| 系统盘 | 1 TB SSD |
| 数据盘 | 2 TB HDD |

开源聊天模型先测试 8B 或 14B 级别，结合量化配置、上下文长度、显存占用和响应延迟确定部署模型。Linux 发行版、ROCm 版本及模型规格在服务器验证后确定。仓库已有 [ROCm Compose 覆盖配置](compose.rocm.yaml)，供 GPU 联调使用。

## 技术选型

| 层 | 选型与职责 |
| --- | --- |
| 前端 | 当前提供轻量浏览器客户端，后续按需要扩展 TypeScript 前端 |
| 后端 | Go，提供命令行入口和网页问答 API |
| Agent Runtime | Eino，负责模型调用、工具编排和运行上下文 |
| 模型层 | 当前通过 Ollama 调用聊天与嵌入模型。Runtime 与模型服务通过接口通信，后续可切换到独立 GPU 服务器 |
| 数据层 | MySQL 是设计中的业务数据存储。当前验证版使用 Qdrant 存储论文片段和向量 |
| 工具层 | 当前提供论文检索，后续接入 Web Search 和 MCP |

## 开发与部署流程

1. **在 Mac 上验证 Agent 主链路。** 当前 CLI 已能把练习摘录写入 Qdrant，从问题生成向量，使用 Eino 调用检索工具，再根据摘录作答并显示论文及页码。
2. **用真实论文补全知识库。** 实现 PDF 解析、分段和索引，保存论文标识、页码及原文。准备固定题集，覆盖有答案、缺证据和来源冲突三类情况，记录答案正确性、引用支持情况和响应耗时。
3. **完善 Agent 编排与应用接口。** 当前 Go API 和网页已支持知识库问答。后续在 Eino 调用链加入中间件和引用校验，再扩展前端与业务存储，按科研任务需要接入 Web Search 与 MCP。
4. **在 Linux 上联调模型与服务。** 在 AMD GPU 服务器上验证 ROCm 和候选开源模型，测量显存与延迟。确认模型服务可独立运行后，通过 Docker Compose 部署 Agent、模型服务与数据服务，并在 Linux 上复跑固定题集。
5. **用评测驱动后续扩展。** 在有稳定基线后试验模型路由，根据任务复杂度选择模型，比较质量与延迟。遇到可独立并行的科研子任务时，再验证任务拆分和多 Agent 协作。

## 运行当前版本

在项目根目录执行。首次需要准备模型和索引。

```sh
docker compose up -d --wait ollama qdrant
docker compose exec ollama ollama pull qwen3:4b
docker compose exec ollama ollama pull qwen3-embedding:0.6b
docker compose run --rm -T agent --index
docker compose run --rm -T agent --ask '一共收集了多少份数据？其中有39份数据不能用，还有多少份能用的数据？'
```

Qwen3 默认关闭思考模式，减少普通查资料问题的生成耗时。需要对比开启思考的表现时，在问答命令前加 `RK_THINK=true`，例如 `RK_THINK=true docker compose run --rm -T agent --ask '实验样本有多少份？'`。

练习资料中的样本数是 `120` 份，预期答案是 `120 - 39 = 81` 份，检索来源为「练习论文 A，第 3 页」。执行 `docker compose run --rm -T agent --retrieve '一共收集了多少份数据？'` 可以单独查看检索到的摘录和相似度。

## 网页客户端

完成上面的模型准备和索引后，在项目根目录启动网页：

```sh
docker compose up -d web
```

在浏览器打开 [http://localhost:8090](http://localhost:8090)。网页可以提交问题，查看答案和检索到的原文。一次提交一个独立问题。网页先检索 Qdrant，再用一次模型调用生成答案。生成未完成时会展示检索原文。命令行的 `--ask` 可继续演示 Eino 的工具调用链路。

默认只监听开发机的本地端口。若 8090 已占用，可用 `RK_WEB_PORT=8091 docker compose up -d web` 切换端口。修改前端文件后，执行 `docker compose restart web` 让 Go 重新编译内嵌页面。

### 在 Proxmox VE 虚拟机中向局域网开放

在 PVE 中创建 Linux 虚拟机，将网卡桥接到接入局域网的网桥（通常是 `vmbr0`），为虚拟机分配固定的局域网 IPv4 地址。在虚拟机里安装 Docker 和 Compose，复制项目，并执行上面的模型准备与索引命令。

假设虚拟机地址为 `192.168.1.50`，在虚拟机上的项目目录创建或更新 `.env`：

```dotenv
RK_WEB_BIND=192.168.1.50
RK_WEB_PORT=8090
```

将示例地址替换为虚拟机的真实地址，可在虚拟机内用 `ip -br -4 addr` 查看。然后运行：

```sh
docker compose up -d web
docker compose ps web
```

同一局域网的设备访问 `http://192.168.1.50:8090`。只发布 Web 的 TCP 8090 端口，Ollama 和 Qdrant 通过 Compose 内部网络访问。如果启用了 PVE 防火墙，为虚拟机放行来自受信任客户端网段的 TCP 8090。网页当前没有登录功能，保持在受信任的局域网内使用，路由器不配置公网端口转发。

看不到页面时，先在虚拟机内请求它的局域网地址和端口，再从另一台局域网设备访问。虚拟机内能打开但其他设备打不开时，检查 PVE 防火墙、VLAN 或 Wi-Fi 客户端隔离。
