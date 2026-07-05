# HTML 报告格式

架构评审在操作系统临时目录中呈现为单个独立的 HTML 文件。 Tailwind 和 Mermaid 都来自 CDN。 Mermaid 可靠地处理图形形状的图表；手工构建的 div 和内联 SVG 可以处理更多的编辑视觉效果（质量地图、横截面）。将两者混合起来——不要所有图都依赖 Mermaid，否则视觉会变得单调。

## 脚手架
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Architecture review — {{repo name}}</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script type="module">
      import mermaid from "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs";
      mermaid.initialize({ startOnLoad: true, theme: "neutral", securityLevel: "loose" });
    </script>
    <style>
      /* small custom layer for things Tailwind doesn't cover cleanly:
         dashed seam lines, hand-drawn-feeling arrow heads, etc. */
      .seam { stroke-dasharray: 4 4; }
      .leak { stroke: #dc2626; }
      .deep { background: linear-gradient(135deg, #0f172a, #1e293b); }
    </style>
  </head>
  <body class="bg-stone-50 text-slate-900 font-sans">
    <main class="max-w-5xl mx-auto px-6 py-12 space-y-12">
      <header>...</header>
      <section id="candidates" class="space-y-10">...</section>
      <section id="top-recommendation">...</section>
    </main>
  </body>
</html>
```
## 标题

仓库名称、日期和紧凑的图例：实心框 = 模块，虚线 = 接缝，红色箭头 = 泄漏，厚暗框 = 深模块。没有介绍段落——直接进入候选人。

## 候选人卡

图表很有分量。散文简洁、朴素，并且使用术语（来自“/codebase-design-lqy”技能）而不拘礼节。

每个候选项都是一个“<article>”：

- **标题** — 简短，命名深化（例如“折叠订单接收管道”）。
- **徽章行** — 推荐强度（“强”= 翡翠，“值得探索”= 琥珀色，“推测”= 石板），加上依赖类别的标签（“进程中”、“本地可替代”、“端口和适配器”、“模拟”）。
- **文件** — 等宽列表，`font-mono text-sm`。
- **之前/之后图表** — 核心部分。两列并排。请参阅下面的模式。
- **问题** — 一句话。什么痛。
- **解决方案** — 一句话。有什么变化。
- **获胜** — 项目符号，每个项目符号≤6 个单词。例如“测试命中一个接口”、“定价逻辑停止泄漏”、“删除 4 个浅层包装器”。
- **ADR 标注**（如果适用）— 琥珀色框中的一行。

没有解释的段落。如果图表需要一段才能理解，请重新绘制图表。

## 图表模式

选择适合候选人的模式。将它们混合起来。不要让每个图表看起来都一样——多样性是重点的一部分。

### Mermaid 图（依赖项/调用流程的主力）

当要点是“X 调用 Y 调用 Z，并查看混乱情况”时，请使用Mermaid `flowchart` 或 `graph`。将其包裹在 Tailwind 风格的卡片中，避免图表像凭空掉进页面里。使用 classDef 进行样式，将泄漏边缘着色为红色，将深模块着色为黑色。序列图适用于“之前：6 次往返；之后：1 次”。
```html
<div class="rounded-lg border border-slate-200 bg-white p-4">
  <pre class="mermaid">
    flowchart LR
      A[OrderHandler] --> B[OrderValidator]
      B --> C[OrderRepo]
      C -.leak.-> D[PricingClient]
      classDef leak stroke:#dc2626,stroke-width:2px;
      class C,D leak
  </pre>
</div>
```
### 手工制作的盒子和箭头（当 Mermaid 布局不听使唤时）

模块为带有边框和标签的“<div>”。作为内联 SVG `<line>` 或 `<path>` 元素的箭头绝对定位在相对容器上。当您希望“之后”图感觉像一个具有灰色内部结构的厚边框深模块时，可以使用此选项 - Mermaid 不会以正确的权重渲染它。

### 横截面（适合分层浅度）

堆叠水平带（`h-12 border-l-4`）以显示呼叫经过的层。之前：6 层薄层，每层什么都不做。之后：1 条粗带，标有综合责任。

### 质量地图（适用于“接口与实现一样宽”）

每个模块有两个矩形——一个用于接口表面积，一个用于实现。之前：接口矩形几乎与实现矩形一样高（浅）。之后：接口矩形较短，实现矩形较高（深）。

### 调用图崩溃

之前：呈现为嵌套框的函数调用树。之后：同一棵树折叠成一个盒子，其中显示的现在内部调用逐渐消失。

## 风格指导

- 精益的社论，而不是公司仪表板。慷慨的空白。标题的衬线可选（“font-serif”适用于石头/石板）。
- 谨慎使用颜色：一种强调色（翠绿色或靛蓝色）加上红色（表示泄漏）和琥珀色（表示警告）。
- 保持图表高度约 320 像素，以便前后可以舒适地并排放置，无需滚动。
- 使用“text-xs uppercase track-wider”作为图表内的模块标签 - 它们应该作为原理图而不是 UI 来阅读。
- 唯一的脚本是 Tailwind CDN 和 Mermaid ESM 导入。该报告是静态的——没有应用程序代码，除了 Mermaid 自己的渲染之外没有交互性。

## 热门推荐部分

一张更大的卡。候选人姓名、一句话说明原因、其卡片的锚链接。就是这样。

## 语气

简单中文，简洁 - 但建筑名词和动词直接来自“/codebase-design-lqy”技能。简洁并不是随波逐流的借口。

**准确使用：**模块、接口、实现、深度、深、浅、接缝、适配器、杠杆、局部性。

**永远不要替代：**组件、服务、单元（对于模块） · API、签名（对于接口） · 边界（对于接缝） · 层、包装器（对于模块，当你指的是模块时）。

**符合风格的短语：**

- “订单接收模块很浅——界面几乎与实现相匹配。”
- “定价完全泄露。”
- “深化：一个接口，一处测试。”
- “两个适配器证明了这一点：生产中的 HTTP，测试中的内存。”

**胜利要点**用术语来命名增益：*“局部性：错误集中在一个模块中”*，*“杠杆：一个接口，N 个调用站点”*，*“接口缩小；实现吸收了包装器”*。不要写*“更容易维护”*或*“更干净的代码”* - 这些术语不在术语表中，也没有它们的位置。

没有对冲，没有清嗓子，没有“值得注意的是……”。如果一句话可以成为一颗子弹，那就让它成为一颗子弹。如果可以切子弹，那就切它。如果某个术语不在“/codebase-design-lqy”词汇表中，请在发明新术语之前先找到该术语。