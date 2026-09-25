/**
 * v2.2 PR4 — Markdown 组件测试。
 *
 * 必须 100% 覆盖的 XSS 5 条用例（PRD §9.1.2 DoD）：
 *   1. `<script>alert(1)</script>` → 脚本元素被剥离
 *   2. `<img src=x onerror=alert(1)>` → onerror 属性 + 不安全 src 被剥离
 *   3. `[click](javascript:alert(1))` → href 协议被剥离 / 转为安全链接
 *   4. `<iframe src=evil.com>` → iframe 元素被剥离
 *   5. `<svg onload=alert(1)>` → svg 元素被剥离
 *
 * 此外覆盖基本渲染（标题 / 列表 / 表格 / 引用 / 行内 code / 代码块 / 链接）
 * + sanitize 默认 schema 集成验证。
 */

import { describe, it, expect } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

import { Markdown } from './Markdown';

afterEach(() => cleanup());

// ---------------------------------------------------------------------------
// 5 条 XSS 用例（PRD §9.1.2 — 极高风险，必 100% 通过）
// ---------------------------------------------------------------------------

describe('Markdown — XSS sanitize 5 用例（PRD §9.1.2 DoD）', () => {
  it('1. <script>alert(1)</script> 必须被剥离（无 <script> 节点）', () => {
    const { container } = render(<Markdown content="<script>alert(1)</script>" />);
    expect(container.querySelector('script')).toBeNull();
    // 容器内不应包含任何 alert 文本（removal 后无残留）
    expect(container.innerHTML.toLowerCase()).not.toContain('alert(1)');
  });

  it('2. <img src=x onerror=alert(1)> 必须剥离 onerror（属性被剥离）', () => {
    const { container } = render(<Markdown content='<img src="x" onerror="alert(1)">' />);
    const imgs = container.querySelectorAll('img');
    if (imgs.length > 0) {
      // 即便保留 <img>，onerror 必须被剥离
      imgs.forEach((img) => {
        expect(img.getAttribute('onerror')).toBeNull();
      });
    } else {
      // 默认 schema 也允许 <img>，所以默认会渲染；onerror 必须剥离
      // 这里不强求 <img> 不存在，只要求 onerror 被剥离
    }
    // 不应在最终 DOM 中存在可执行的 onerror handler
    expect(container.querySelector('[onerror]')).toBeNull();
  });

  it('3. [click](javascript:alert(1)) 链接必须被剥离/替换为安全链接', () => {
    const { container } = render(<Markdown content="[click](javascript:alert(1))" />);
    const anchors = container.querySelectorAll('a');
    if (anchors.length > 0) {
      anchors.forEach((a) => {
        const href = a.getAttribute('href') || '';
        expect(href.toLowerCase()).not.toMatch(/^javascript:/i);
        expect(href.toLowerCase()).not.toContain('alert(1)');
      });
    }
    // 即便 anchor 被完全剥离，文本"click"可能被保留为纯文本（这是可接受的）
    expect(container.innerHTML.toLowerCase()).not.toContain('javascript:alert');
  });

  it('4. <iframe src=evil.com> 必须被剥离', () => {
    const { container } = render(<Markdown content='<iframe src="evil.com"></iframe>' />);
    expect(container.querySelector('iframe')).toBeNull();
    expect(container.innerHTML).not.toContain('evil.com');
  });

  it('5. <svg onload=alert(1)> 必须被剥离', () => {
    const { container } = render(<Markdown content='<svg onload="alert(1)"></svg>' />);
    expect(container.querySelector('svg')).toBeNull();
    expect(container.querySelector('[onload]')).toBeNull();
    expect(container.innerHTML.toLowerCase()).not.toContain('alert(1)');
  });

  it('综合：含多个 vector 的混合输入全部被 sanitize', () => {
    const evil = [
      '<script>alert(1)</script>',
      '<img src=x onerror=alert(2)>',
      '[bad](javascript:alert(3))',
      '<iframe src="evil.com"></iframe>',
      '<svg onload="alert(4)"></svg>',
      '<a href="javascript:alert(5)">x</a>',
      '<button onclick="alert(6)">click</button>',
    ].join('\n');
    const { container } = render(<Markdown content={evil} />);
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('iframe')).toBeNull();
    expect(container.querySelector('svg')).toBeNull();
    expect(container.querySelector('[onerror]')).toBeNull();
    expect(container.querySelector('[onload]')).toBeNull();
    expect(container.querySelector('[onclick]')).toBeNull();
    container.querySelectorAll('a').forEach((a) => {
      const href = a.getAttribute('href') || '';
      expect(href.toLowerCase()).not.toMatch(/^javascript:/i);
    });
  });
});

// ---------------------------------------------------------------------------
// 基本渲染（标题 / 列表 / 表格 / 引用 / 行内 code / 代码块 / 链接）
// ---------------------------------------------------------------------------

describe('Markdown — 基本渲染', () => {
  it('标题：H1-H4 正确渲染', () => {
    const md = ['# H1', '## H2', '### H3', '#### H4'].join('\n');
    const { container } = render(<Markdown content={md} />);
    expect(container.querySelector('h1')?.textContent).toBe('H1');
    expect(container.querySelector('h2')?.textContent).toBe('H2');
    expect(container.querySelector('h3')?.textContent).toBe('H3');
    expect(container.querySelector('h4')?.textContent).toBe('H4');
  });

  it('列表：有序 + 无序', () => {
    const md = ['- item A', '- item B', '', '1. one', '2. two'].join('\n');
    const { container } = render(<Markdown content={md} />);
    const ul = container.querySelector('ul');
    const ol = container.querySelector('ol');
    expect(ul).not.toBeNull();
    expect(ol).not.toBeNull();
    expect(ul?.querySelectorAll('li').length).toBe(2);
    expect(ol?.querySelectorAll('li').length).toBe(2);
  });

  it('表格（GFM）', () => {
    const md = '| Name | Age |\n|------|-----|\n| A    | 30  |\n| B    | 40  |';
    const { container } = render(<Markdown content={md} />);
    expect(container.querySelector('table')).not.toBeNull();
    const rows = container.querySelectorAll('tr');
    expect(rows.length).toBe(3); // header + 2 data rows
  });

  it('引用（blockquote）', () => {
    const { container } = render(<Markdown content="> 这是一段引用" />);
    const bq = container.querySelector('blockquote');
    expect(bq).not.toBeNull();
    expect(bq?.textContent).toContain('这是一段引用');
  });

  it('行内 code（`code`）', () => {
    const { container } = render(<Markdown content="使用 `npm install` 安装" />);
    const code = container.querySelector('code');
    expect(code).not.toBeNull();
    expect(code?.textContent).toBe('npm install');
    // 行内 code 应没有 pre 包裹
    expect(container.querySelector('pre')).toBeNull();
  });

  it('代码块（fenced ```）— 带语言标签 + 复制按钮', () => {
    const md = '```python\nprint("hello")\n```';
    const { container, getByTestId } = render(<Markdown content={md} />);
    const codeBlock = getByTestId('code-block');
    expect(codeBlock).not.toBeNull();
    // 语言标签
    expect(codeBlock.textContent).toContain('python');
    // 复制按钮
    const copyBtn = getByTestId('code-block-copy');
    expect(copyBtn).not.toBeNull();
    expect(copyBtn.textContent).toContain('复制');
    // 代码内容
    expect(container.querySelector('pre')).not.toBeNull();
    expect(container.querySelector('code')).not.toBeNull();
  });

  it('链接自动 target=_blank + rel=noopener noreferrer', () => {
    const { container } = render(<Markdown content="[baidu](https://baidu.com)" />);
    const a = container.querySelector('a');
    expect(a).not.toBeNull();
    expect(a?.getAttribute('target')).toBe('_blank');
    expect(a?.getAttribute('rel')).toContain('noopener');
    expect(a?.getAttribute('rel')).toContain('noreferrer');
  });

  it('加粗 + 斜体 + 删除线（GFM）', () => {
    const { container } = render(<Markdown content="**bold** *italic* ~~strike~~" />);
    expect(container.querySelector('strong')?.textContent).toBe('bold');
    expect(container.querySelector('em')?.textContent).toBe('italic');
    expect(container.querySelector('del')?.textContent).toBe('strike');
  });
});

// ---------------------------------------------------------------------------
// 组件契约
// ---------------------------------------------------------------------------

describe('Markdown — 组件契约', () => {
  it('默认渲染包含 data-testid="markdown"', () => {
    const { container } = render(<Markdown content="hello" />);
    const md = container.querySelector('[data-testid="markdown"]');
    expect(md).not.toBeNull();
  });

  it('className 注入（追加在 ic-markdown 之后）', () => {
    const { container } = render(<Markdown content="hello" className="custom-cls" />);
    const md = container.querySelector('[data-testid="markdown"]');
    expect(md?.className).toContain('ic-markdown');
    expect(md?.className).toContain('custom-cls');
  });

  it('空字符串不崩', () => {
    expect(() => render(<Markdown content="" />)).not.toThrow();
  });

  it('纯文本（无 markdown 标记）正常渲染', () => {
    render(<Markdown content="just plain text" />);
    expect(screen.getByText('just plain text')).toBeDefined();
  });
});
