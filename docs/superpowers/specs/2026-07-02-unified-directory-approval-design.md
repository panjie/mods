# 统一目录审批设计 (Unified Directory-Centric Approval)

此方案已被替代；旧目录边界、固定意图标签及对应实现示例已删除。

当前行为：

- 进程当前目录只用于相对路径解析和工具的默认执行位置。
- 只读操作不因路径位置触发审批。
- 写入按具体本地目录或远程 origin 授权；授权保存在当前会话中。
- 临时目录写入例外仍需明确的写入目标和已知操作效果。

参见 [写入目标审批设计](2026-09-03-write-target-approval-design.md)
和 [写入目标推断设计](2026-09-11-write-target-inference-design.md)。
