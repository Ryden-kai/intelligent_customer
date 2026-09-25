/**
 * v2.2 PR4 — Markdown 渲染组件。
 *
 * 关键依赖与作用：
 *   - react-markdown：把 Markdown 文本 → React 元素树。
 *   - remark-gfm：支持 GitHub Flavored Markdown（表格 / 任务列表 / 删除线 / 自动链接）。
 *   - rehype-highlight：代码块语法高亮（highlight.js）。
 *   - rehype-sanitize：XSS 防护（**必须开启**，PRD R2 极高风险；5 条用例必过）。
 *   - DOMPurify：rehype-sanitize 内部使用，无需直接 import。
 *
 * 安全策略：
 *   - 链路：Markdown 文本 → remark 解析 → rehype 树 → sanitize（白名单 schema）
 *     → highlight 注入 className → react-markdown 渲染 React 元素。
 *   - sanitize 默认 schema 已剔除 <script> / <iframe> / onerror / onclick 等。
 *   - <a> 强制 target="_blank" rel="noopener noreferrer"，避免反向 tabnabbing。
 *   - <pre> 自定义：包裹 CodeBlock（带语言标签 + 复制按钮）。
 *
 * react-markdown 9.x 适配：
 *   - 已移除 `inline` prop；改为通过 `pre` 与 `code` 区分（pre 包 code = 块级）。
 *
 * 组件复用：
 *   - Chat Bubble / Jev 模板预览 / 任何需要 Markdown 渲染的地方。
 */

import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize';

import { CodeBlock } from './CodeBlock';

interface Props {
  /** Markdown 原文 */
  content: string;
  /** 注入额外 className（默认 'ic-markdown'） */
  className?: string;
}

/**
 * 提取 rehype-highlight 给出的 language 类名（如 `language-python` → `python`）。
 */
function extractLanguage(className?: string): string | undefined {
  if (!className) return undefined;
  const m = /language-([\w+-]+)/.exec(className);
  return m?.[1];
}

/**
 * 从 React 节点中提取纯文本字符串（用于复制按钮）。
 * react-markdown 9.x 的 children 是 React 节点数组，需手动 flatten。
 */
function extractText(children: React.ReactNode): string {
  if (children == null || children === false) return '';
  if (typeof children === 'string') return children;
  if (typeof children === 'number') return String(children);
  if (Array.isArray(children)) return children.map(extractText).join('');
  if (typeof children === 'object' && 'props' in children) {
    const props = (children as { props?: { children?: React.ReactNode } }).props;
    return extractText(props?.children);
  }
  return '';
}

/**
 * 自定义 components 映射（typed）。
 *  - `a` 强制 target=_blank rel=noopener noreferrer
 *  - `code` 行内保持原生；块级由 `pre` 组件接管
 *  - `pre` 块级：识别语言 + 注入 CodeBlock（带复制按钮）
 */
const components: Components = {
  a: ({ node: _node, ...props }) => (
    // 强制外链新窗口打开；rel 防止 tabnabbing。
    <a {...props} target="_blank" rel="noopener noreferrer" />
  ),
  pre: ({ children, node: _node, ...props }) => {
    // react-markdown 9.x：块级 code 会渲染为 <pre><code class="language-xxx hljs">...</code></pre>。
    // 我们拦截 <pre>，把内部 <code> 的 props 抽出来，包 CodeBlock。
    // children 通常是单个 <code> React 元素（hast-util-to-jsx-runtime 生成）。
    let codeClassName: string | undefined;
    let codeText = '';
    let codeNode: React.ReactNode = children;

    const childProps = (children && typeof children === 'object' && 'props' in children)
      ? (children as { props?: { className?: string; children?: React.ReactNode } }).props
      : undefined;
    if (childProps) {
      codeClassName = childProps.className;
      codeText = extractText(childProps.children);
      codeNode = childProps.children;
    } else {
      codeText = extractText(children);
    }
    const language = extractLanguage(codeClassName);
    return (
      <CodeBlock
        language={language}
        code={codeText}
        codeClassName={codeClassName}
      >
        {codeNode}
      </CodeBlock>
    );
  },
};

export function Markdown({ content, className }: Props) {
  // 为 sanitize schema 添加 `className` 属性支持（高亮 class 不会被剥离）。
  const schema: typeof defaultSchema = {
    ...defaultSchema,
    attributes: {
      ...defaultSchema.attributes,
      // 允许 code 元素带 hljs / language-* class（高亮必需）
      code: [
        ...(defaultSchema.attributes?.code ?? []),
        ['className'],
      ],
      // 允许 span 带 className（highlight.js 内部 token）
      span: [
        ...(defaultSchema.attributes?.span ?? []),
        ['className'],
      ],
      // 允许 pre 带 className（部分插件会加）
      pre: [
        ...(defaultSchema.attributes?.pre ?? []),
        ['className'],
      ],
    },
  };

  return (
    <div
      className={['ic-markdown', className].filter(Boolean).join(' ')}
      data-testid="markdown"
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[
          // 先高亮再 sanitize（sanitize 会剥离额外属性，所以顺序很关键）。
          rehypeHighlight,
          // 必须！默认 schema 已覆盖常见 XSS vector。
          // 5 条用例见 Markdown.test.tsx（<script> / <img onerror> / javascript: / <iframe> / <svg onload>）。
          [rehypeSanitize, schema],
        ]}
        components={components}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}

export default Markdown;
