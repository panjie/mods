# 人工 UI 验收（待操作者填写）

2026-10-28：用户明确选择本轮暂不操作；以下全部人工项目保持待验收。

`manual-ui.py` 在本机回环地址运行固定响应服务，以真实 `bin/mods.exe` 展示生产审批界面。它不调用外部模型；每次创建独立临时合成目录，复制二进制并使用隔离配置。`review-mode: always` 保证固定写入也显示审批，`command-simplification: off` 保留人工 S。该模式只用于 UI，不属于非交互 auto 模型对照。

请在真实 Windows Terminal / PowerShell 终端运行（不要重定向输出）：

```powershell
cd C:\Users\panjie\dev\mods
$ui = 'docs/superpowers/evidence/2026-10-28-command-generation-p5-windows/manual-ui.py'
python $ui unknown --host ps7
python $ui write --host ps7
python $ui incomplete --host ps7
python $ui pages --host ps7
python $ui preview --host ps7
python $ui change --host ps7
```

将 `--host ps7` 改成 `--host ps51` 可验收 PS5.1。宿主在启动时打印；PS5.1 会从子进程 PATH 排除全部 pwsh 目录。固定调用保存在每次打印的临时目录 `fixed-call.json`，可在操作前审阅。只有 `write`/`incomplete` 会写入该次新建合成目录；其余固定命令仅打印常量。

| 检查 | 操作 | 结果 |
| --- | --- | --- |
| unknown 文案 | `unknown`：确认说明效果未证明，不暗示已发现写入；按 N | 待人工 |
| known write 文案 | `write`：确认写入目标和授权范围；按 Y，检查合成目标文件 | 待人工 |
| incomplete 文案 | `incomplete`：动态目标应展示已知写入/目标不完整；按 N | 待人工 |
| 分页/门槛 | `pages`：未看完先按 Y，确认不能批准；翻完所有页，确认允许批准 | 待人工 |
| 人工 S | 另起 `pages`，未看完时按 S；确认不执行，得到重新组织反馈 | 待人工 |
| N | 另起 `preview` 按 N；确认无执行输出 | 待人工 |
| Ctrl+C | 另起 `preview` 按 Ctrl+C；确认取消，不作为普通拒绝继续模型 | 待人工 |
| 窄终端/中文/长路径 | 终端缩到约 50 列运行 `write`，调整窗口，确认目标完整可读 | 待人工 |
| 文件预览 | `preview`：确认编号源码、来源 unknown、预览限制、完整浏览门槛 | 待人工 |
| 变化一次 | `change`：停在首个审批页，在另一个终端执行下述固定修改，再批准原页；确认重审新内容一次 | 待人工 |
| 持续变化 | 另起 `change`，第一次修改后进入第二次审批，再修改一次并批准；确认停止执行 | 待人工 |

固定修改命令（`<run-directory>` 替换为启动时打印的合成目录）：

```powershell
python docs/superpowers/evidence/2026-10-28-command-generation-p5-windows/manual-ui.py change --mutate '<run-directory>'
```

修改器仅接受本机临时目录下具有专用标记的 `mods-p5-ui-*` 目录，固定追加 `Write-Output 'FIXED-SYNTHETIC-CHANGED'`。不要改成用户脚本或真实文件。

请回报：实际 host、终端应用、宽×高、case、按键、显示行为、是否执行/取消、问题。可以附上合成目录的 `observed-tool-results.json`；它只是普通协议观察，不能当作可信 typed control/dispatch 计数。人工 S 的结果需结合操作者观察记录，不从任意 JSON 文本推导通过。

自动 launcher smoke 仅确认固定服务和非交互阻断可用，不构成人工通过。所有人工项在收到实际结果前保持待验收。
