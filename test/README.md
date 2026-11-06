# 项目测试

所有自维护测试源码、夹具和入口集中在本目录，按模块命名，禁止继续使用 `core_suite_2/3/4/5` 一类编号分组。

```text
test/
  run.mjs                 统一入口、布局检查、临时根清理
  layout.json             后端测试模块与生产包对应关系
  backend/
    suite_test.go         标准 Go 测试入口及临时 overlay 适配
    race_*_test.go        将父调用的 race 状态传给真实子套件
    core/                 项目存储、模型、讨论、写作、上下文、Wiki、图谱、任务等
    preview/              HTTP 信封、来源与访问边界
    platform/             目录兼容和实例文件锁
  frontend/
    fixtures.py           临时服务与模型/搜索/记忆的外部边界夹具
    test_studio_ui.py      Playwright 完整流程与控件验收入口
    studio_ui_cases.js     具体浏览器断言
    test_midscene_ui.py    Midscene 临时依赖与视觉验收入口
    midscene_ui_cases.mjs  具体视觉操作及业务断言
  archive/
    _specula-lite/        内容不改写的历史审查模型、探针及证据
```

## 执行

从项目根目录运行：

```sh
npm test
npm run test:core
npm run test:race
npm run check:go
npm run check:test-layout
npm run test:ui
npm run test:midscene
```

按模块或用例运行、保留一次有意交付的 JSON 结果：

```sh
node test/run.mjs preview
node test/run.mjs platform
node test/run.mjs core --case TestModelSettingsReloadAcrossEngines
node test/run.mjs go --report /tmp/quillweave-tests-new-run.json
node test/run.mjs midscene --credentials-only --result /tmp/quillweave-keys-new-run.json
```

Midscene默认运行完整创作流程；`--credentials-only`只运行密钥输入、保存遮蔽和清除的视觉用例，并核对真实后端状态。它不是完整创作流程的通过证据。

`--short` 传入真实子测试；`--race`（或 `race` 模式）使父适配器和实际业务套件都以竞态检测构建。`--json` 输出 Go 父适配器事件，业务用例的真实数量、名称和执行状态在 `BACKEND_RESULT` 记录及显式 JSON 报告中，不能把适配器数量当作原业务测试数量。报告使用新文件名，拒绝覆盖旧证据。

## Go 包内测试的编译方式

Go 私有实现只能由同包源码访问。集中后的 Go 文件保留原包名，并标记 `quillweave_whitebox`；它们是原包的测试源码，不是独立生产包。适配器在临时目录生成 Go 原生 overlay，把这些源文件临时关联到对应 `internal` 包的编译视图中。磁盘上不向生产目录复制或留下测试文件，也不新增生产测试接口。

普通 `go test ./...` 或 `go test ./test/backend -count=1` 仍会通过适配器实际运行全部后端模块；可用 `-run TestBackendModules/core` 选择模块。旧的 `go test ./internal/core` 已不再是测试入口。需要单用例筛选时使用上面的统一入口 `--case`。

适配器读取测试声明并核对实际顶层终态，空模块、退出成功却零用例、漏执行或非零子进程都不能通过。读取源码使默认 Go 缓存依赖这些外置测试文件，生产包依赖也在入口中保留。竞态模式通过编译标签明确传递，不只检测包装器。

`npm run check:go` 同时检查生产/适配器包及带 overlay 的全部白盒测试；仅运行普通 `go vet ./...` 不覆盖这些带专用标签的源文件。CI 调用统一入口和布局检查，1000行检查覆盖当前测试目录。

## 隔离、历史与范围

后端统一入口使用独立临时根，并在失败/中断后清理；前端两种入口沿用自己的 TemporaryDirectory/finally。外部服务夹具不代表真实文学质量或搜索准确性；Midscene 视觉模型是真实调用。业务断言和用例内容在迁移中保持，新增的是目录/适配责任。

历史审查整体放在 `archive/_specula-lite`，下划线使 Go 默认包扫描跳过历史缺陷探针。它不属于当前正常测试，不把“复现缺陷成功”计为修复通过。本机隐藏的 `.specula-lite` 只是旧报告路径的兼容符号链接，实际文件全部在本目录内；归档内容逐文件校验保持原样。历史运行器依赖原源码清单与布局，仅作当时证据；当前复跑使用本文件的统一入口。
