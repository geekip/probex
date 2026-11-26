# WebUI 源码

`src/main.ts` 挂载应用。源码按功能组织，跨目录使用 `@/` 指向 `src/`，直接导入具体模块，不设置聚合转发文件。

| 目录                      | 职责                                                                                                    |
| ------------------------- | ------------------------------------------------------------------------------------------------------- |
| `src/app/`                | 应用状态与页面编排、设置菜单、工作区标签栏；`styles/` 为主题、公共控件与页面布局                        |
| `src/features/nodes/`     | 节点类型、概览统计、节点信息与历史、加入与节点设置                                                      |
| `src/features/terminal/`  | xterm 会话、PaneForge 分屏和布局类型                                                                    |
| `src/features/files/`     | 文件 API、扁平列表、排序、剪贴板与拖放操作；`editor/` 为多文件窗口、CodeMirror、新行处理及媒体/PDF 预览 |
| `src/features/transfers/` | 页面级上传队列、系统选择器和全局上传进度；下载交给浏览器                                                |
| `src/features/forwards/`  | 当前节点的 TCP/UDP 映射列表和添加表单                                                                   |
| `src/shared/`             | HTTP 请求与认证失效通知、偏好设置、公共确认框；`i18n/` 分开保存中英文词条                               |
| `mock/`                   | 开发用内存 API、终端与媒体样本；仅 `dev:mock` 启用                                                      |

节点领域类型位于 `features/nodes/types.ts`，文件契约位于 `features/files/api.ts`，不通过公共 HTTP 客户端转发领域类型。公共模块不导入功能组件。各功能的样式位于自己的目录，应用统一引入；CodeMirror 和 PDF.js 仍按需加载。

三处标签共用 `src/shared/tabs.css` 的圆角、高亮样式与 `src/shared/tab-sort.ts` 的 Interact.js 拖动排序；也可用 Alt+Shift+左右方向键移动标签。右侧栏使用 mini 尺寸，排序保持 keyed 组件实例。

文件列表使用 Selecto 框选，状态留在文件管理组件中。名称用于拖动移动，其他单元格和空白处用于框选；行单击选择、双击打开，Ctrl/Meta/Shift 保持多选。拖动预览复用 Pragmatic，单项显示名称，多项显示数量。`licenses/` 保留 keycon 和 css-styled 的上游 MIT 许可，补齐 npm 包遗漏的许可文件。

在此目录执行 `npm run check`、`npm run format:check` 和 `npm run build`。Mock 使用与范围见 [mock/README.md](mock/README.md)。
