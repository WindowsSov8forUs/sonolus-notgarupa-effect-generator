# sonolus-notgarupa-effect-generator

生成 NotGarupa 的全部音效条目：原作的判定音（Perfect / Great / Good、Flick、方向键 1–3 连）、空击、技能、Long 按住音与松开、Full Combo / Clear / 通关语音等，名称与原作 cue 名一致，另附 Sonolus 标准名（`#PERFECT`、`#GREAT`、`#GOOD`、`#HOLD` 等）作为别名。

默认运行即生成完整资源，没有选择单个条目的参数。

## 条目

条目由原作 JP 10.1.4 的皮肤主数据决定（`internal/master`）：玩家能用到的每个打击音皮肤（SE 设置）对应一个条目 `notgarupa-<SE 皮肤>`，共 15 个：

- 普通 SE 设置：`skin00`–`skin03`；
- 特殊皮肤（Collaboration / Limited）的 SE 组件：`skin_persona`、`skin_miku`、`skin_cafe`、`skin_maid`、`skin_coin`、`skin_witch`、`skinapril2021`、`skin_stage`、`skin_delta`、`skin_april2018`、`skin_april2019`。

方向键音效在原作中固定为 `directionalflickskin00`，所有条目相同；`sound/common` 的技能、Full Combo、Clear 等 cue 与 APK 内置的 `decide_1` 也相同。主数据中无法获取的条目已整体排除（见 `internal/master` 的 `Excluded`）。

## 素材来源

- 音频：Bestdori 的 MP3（`sound/tapseskin/<id>`、`sound/common` 等，按 `-regions` 的服务器优先级取第一个拥有该 bundle 的服务器），无缝解码为 PCM（去掉 MP3 编码器延迟，起点与原作一致），输出 WAV。只有 APK 内置的 `decide_1`（不在 AssetBundle 中）随项目放在 `assets/apk/sound/common-se/`。
- 循环点与同时发声上限：原作 cue sheet。官方资源 CDN 的 `sound/tapseskin/<id>` bundle 中的 TextAsset `TapSE.acb`（方向键为 `DirectionalFlickSE.acb`），程序解析 CRI @UTF 表，沿 Cue → Sequence（上限命令）→ Track → Synth → Waveform（→ WaveformExtensionData 循环区间）读取。资源版本固定为 `internal/cdn` 的 `PinnedVersion`。

下载都写入 `-cache`（默认当前目录的 `.cache/`）。

## 按住音

`SE_RHYTHM_TAP_LONG` 按各条目自己的 cue sheet 处理（采样率 32 / 44.1 / 48 kHz 均可）：

- `SE_RHYTHM_TAP_LONG`：整个 cue。
- `#loop`：原作循环区间 `[LoopStart, LoopEnd)` 的采样，可无缝 PlayLooped。
- `#intro`：只有循环起点不为 0 时才有（`skin_maid`、`skin_coin`、`skinapril2021`、`skin_stage`、`skin_delta`），即 `[0, LoopStart)`；先播放它，`introSeconds` 后开始播放 `#loop`。
- `#release`：松开时的 0.3 s 线性淡出尾（原作代码常量）。
- `skin_april2019` 的按住 cue 不循环（只播放一次），没有 `#loop`，`#HOLD` 指向整个 cue。
- 标记 clip（1 采样的静音）：Sonolus 引擎读不到 clip 时长与条目元数据，所以按住音的条目属性以 clip 是否存在表示，引擎在初始化时读取：`notgarupa/hold/introUsBit<k>` 为 `#intro` 时长（µs）的第 k 位，`notgarupa/hold/onceUsBit<k>` 为不循环的 cue 时长（µs）的第 k 位，`notgarupa/hold/noCueLimit` 表示按住 cue 没有同时发声上限（`skin_delta`）。skin00 等普通条目没有标记。

Miss 无音效（生成静音以便别名完整）。

## 命令行

从 GitHub Release 下载对应平台的压缩包（linux / windows / darwin，amd64 / arm64；由 `.github/workflows/build.yml` 矩阵编译，推送 `v*` 标签时发布），解压后直接运行；也可以用 Go 安装或在本仓库中运行（仓库为私有，需要 `GOPRIVATE=github.com/WindowsSov8forUs/*` 和可访问仓库的 git 凭据）：

```sh
go install github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/cmd/sonolus-notgarupa-effect-generator@latest
go run ./cmd/sonolus-notgarupa-effect-generator   # 在本仓库中
```

```sh
sonolus-notgarupa-effect-generator
```

输出到 `out/`：

- `out/source`：sonolus-pack 源目录；
- `out/pack`：用 sonolus-pack-go 打包后的资源包，可直接被服务器加载；
- `out/contracts/<条目>.json`：每个条目的契约（clip 列表、按住音的采样率 / 循环区间 / `introSeconds`、各 cue 的同时发声上限 `cueLimits`，-1 表示 cue 未设上限）。

参数只用于特殊场景：

| 参数 | 默认值 | 作用 |
|---|---|---|
| `-out` | `out` | 输出目录，其中的 `source/`、`pack/`、`contracts/` 每次重建 |
| `-cache` | `.cache` | 下载缓存目录 |
| `-offline` | false | 只使用缓存中的下载 |
| `-regions` | `jp,en,tw,cn,kr` | Bestdori 服务器优先级。各服有独占素材，每个 bundle 取自第一个拥有它的服务器（该 bundle 的全部文件都来自同一服）；某服没有时记录在缓存中，`-offline` 重跑时选择相同的服务器 |
| `-asset-version` | 固定版本 | 官方资源 CDN 的资源版本（cue sheet） |
| `-j` | 0（每个 CPU 一个） | 并行生成的条目数 |
| `-v` | false | 输出下载日志 |
| `-version` | false | 输出版本号后退出 |

## 作为 Go 库

模块根包 `effect` 提供与命令行相同的生成功能，不写文件、不退出进程、不使用 sonolus-pack-go，便于在服务器启动时一次性生成：

```go
import effect "github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator"

names, err := effect.Items() // 全部条目名，生成前即可得到
err = effect.Generate(ctx, effect.Options{
	Source: effect.Source{CacheDir: "cache/notgarupa"},
}, func(r *effect.Result) error {
	// 每个条目完成后串行调用一次
	return nil
})
```

- `Options`：`Source` 控制下载（`CacheDir`、`Regions`、`AssetVersion`、`Offline`、`HTTPClient`、`Log`），`Workers` 为并行数（0：每个 CPU 一个）。`Generate` 的 `ctx` 可取消下载，遇到第一个错误即返回。多个生成器可以共用同一个 `CacheDir`（缓存写入是原子的）。
- `*effect.Result`：`Item`（sonolus-core-go 的 `database.DatabaseEffectItem`，资源字段留空）、Data（`resource.EffectData`）、Audio（音频 zip）、Thumbnail（PNG）与 Contract。
- `Result.Files()` 按 item.json 的字段名返回资源字节："thumbnail"、"data"（已 gzip 压缩）、"audio"。
- `effect.CreateSourceTree(dir)` 重建 sonolus-pack 源目录，`(*SourceTree).Add(r)` 写入一个条目（命令行就是 `Generate` + `SourceTree` + sonolus-pack-go）。

在 sonolus-server-go 中注册（作为依赖同样需要 `GOPRIVATE`；资源字段填入仓库地址）：

```go
err = effect.Generate(ctx, opts, func(r *effect.Result) error {
	files, err := r.Files()
	if err != nil {
		return err
	}
	it := r.Item // database.DatabaseEffectItem，资源字段待填
	it.Thumbnail, it.Data, it.Audio = s.AddBytes(files["thumbnail"], ""), s.AddBytes(files["data"], ""), s.AddBytes(files["audio"], "")
	s.Effect.Items = append(s.Effect.Items, sonolusserver.EffectItemModel{DatabaseEffectItem: it})
	return nil
})
```

公共代码（Bestdori / CDN 下载与缓存、Unity bundle 与 CRI cue sheet 解析、音频解码、打包输出等）放在本项目 `internal/` 下，项目可单独构建与运行。资源名称约定见 `sonolus-notgarupa-engine/docs/RESOURCE_CONTRACT.md`。
