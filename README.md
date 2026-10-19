# sonolus-notgarupa-effect-generator

生成 NotGarupa 音效包：原作的判定音（Perfect / Great / Good、Flick、方向键 1–3 连）、空击、技能、Long 按住循环与松开、Full Combo / Clear / 通关语音等，名称与原作 cue 名一致，另附 Sonolus 标准名（`#PERFECT`、`#GREAT`、`#GOOD`、`#HOLD` 等）作为别名。运行时写出 `contract.json`。

## 命令行

从 GitHub Release 下载对应平台的压缩包（linux / windows / darwin，amd64 / arm64；由 `.github/workflows/build.yml` 矩阵编译，推送 `v*` 标签时发布），解压后直接运行；也可以用 Go 安装或在本仓库中运行（仓库为私有，需要 `GOPRIVATE=github.com/WindowsSov8forUs/*` 和可访问仓库的 git 凭据）：

```sh
go install github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/cmd/sonolus-notgarupa-effect-generator@latest
go run ./cmd/sonolus-notgarupa-effect-generator [参数]   # 在本仓库中
```

```sh
sonolus-notgarupa-effect-generator [-tapse skin00] [-directional-se directionalflickskin00] [-format wav|mp3] [-name ...] [公共参数]
```

程序内置所需的 APK 资源，其余素材联网下载到 `-cache`（默认当前目录的 `.cache/`）。`-version` 输出版本号。

- `-format wav`（默认）：无缝解码后的 PCM，起点与原作一致（MP3 编码器延迟已去除）；`mp3`：原始字节。
- 来源：Bestdori（`sound/tapseskin/*`、`sound/common` 等，按 `-regions` 的服务器优先级取第一个拥有该 bundle 的服务器），运行时下载到 `.cache/`；只有 APK 内置的 `decide_1`（不在 AssetBundle 中）随项目放在 `assets/apk/sound/common-se/`。
- `SE_RHYTHM_TAP_LONG#loop` 为循环段（0–22997 采样），`#release` 为 0.3 s 淡出尾。Miss 无音效（生成静音以便别名完整）。

## 作为 Go 库

模块根包 `effect` 提供与命令行相同的生成功能，不写文件、不退出进程、不使用 sonolus-pack-go，便于嵌入服务器：

```go
import effect "github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator"

r, err := effect.Generate(ctx, effect.Options{
	Source: effect.Source{CacheDir: "cache/notgarupa", Region: "jp"},
})
```

- `Options` 的零值即命令行的默认值：`TapSE`、`DirectionalSE`、`Format`（`effect.WAV` / `effect.MP3`）、`Name`。
- `Source` 控制下载：`CacheDir`、`Regions`（服务器优先级，默认 jp、en、tw、cn、kr）、`Offline`、`HTTPClient`、`Log`；`Generate` 的 `ctx` 可取消下载。多个生成器可以共用同一个 `CacheDir`（缓存写入是原子的）。
- 返回 `*effect.Result`：`Item`（sonolus-core-go 的 `database.DatabaseEffectItem`，资源字段留空）、Data（`resource.EffectData`）、Audio（音频 zip）、Thumbnail（PNG）与 Contract（即 `contract.json` 的内容）。
- `Result.Files()` 按 item.json 的字段名返回资源字节："thumbnail"、"data"（已 gzip 压缩）、"audio"。
- `effect.WriteSource(dir, results...)` 把结果写成 sonolus-pack 源目录（命令行就是 `Generate` + `WriteSource` + sonolus-pack-go）。

在 sonolus-server-go 中注册（作为依赖同样需要 `GOPRIVATE`）（资源字段填入仓库地址）：

```go
files, err := r.Files()
if err != nil { /* … */ }
it := r.Item // database.DatabaseEffectItem，资源字段待填
it.Thumbnail, it.Data, it.Audio = s.AddBytes(files["thumbnail"], ""), s.AddBytes(files["data"], ""), s.AddBytes(files["audio"], "")
s.Effect.Items = append(s.Effect.Items, sonolusserver.EffectItemModel{DatabaseEffectItem: it})
```

## 公共参数（命令行）

| 参数 | 默认值 | 作用 |
|---|---|---|
| `-out` | `out/source` | 重新生成的 sonolus-pack 源目录 |
| `-pack` | `out/pack` | 用 sonolus-pack-go 打包到该目录（空字符串则跳过） |
| `-cache` | `.cache` | 下载缓存目录 |
| `-regions` | `jp,en,tw,cn,kr` | Bestdori 服务器优先级。各服有独占素材，每个 bundle 取自第一个拥有它的服务器（该 bundle 的全部文件都来自同一服）；某服没有时记录在缓存中，`-offline` 重跑时选择相同的服务器 |
| `-offline` | false | 只使用缓存中的下载 |
| `-v` | false | 输出下载日志 |
| `-version` | false | 输出版本号后退出 |

公共代码（Bestdori 下载与缓存、音频解码、打包输出等）放在本项目 `internal/` 下，项目可单独构建与运行。资源名称约定见 `sonolus-notgarupa-engine/docs/RESOURCE_CONTRACT.md`。
