/**
 * v2.2 PR3 — ThemeToggle 组件测试。
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ThemeToggle } from './ThemeToggle';

function clearStorage() {
  try {
    localStorage.clear();
  } catch {
    /* ignore */
  }
  document.documentElement.classList.remove('dark');
}

describe('ThemeToggle', () => {
  beforeEach(() => {
    clearStorage();
  });
  afterEach(() => {
    clearStorage();
  });

  it('渲染按钮 + data-theme-mode 属性（默认 system）', () => {
    render(<ThemeToggle />);
    const btn = screen.getByTestId('theme-toggle');
    expect(btn).toBeInTheDocument();
    expect(btn.getAttribute('data-theme-mode')).toBe('system');
  });

  it('aria-label 含当前模式 + 下一模式提示', () => {
    render(<ThemeToggle />);
    const btn = screen.getByTestId('theme-toggle');
    const label = btn.getAttribute('aria-label') || '';
    expect(label).toMatch(/主题：/);
    expect(label).toMatch(/切换到/);
  });

  it('点击后 mode 切到 light', () => {
    render(<ThemeToggle />);
    const btn = screen.getByTestId('theme-toggle');
    fireEvent.click(btn);
    expect(btn.getAttribute('data-theme-mode')).toBe('light');
  });

  it('点击三次回到 system', () => {
    render(<ThemeToggle />);
    const btn = screen.getByTestId('theme-toggle');
    fireEvent.click(btn); // system → light
    fireEvent.click(btn); // light → dark
    fireEvent.click(btn); // dark → system
    expect(btn.getAttribute('data-theme-mode')).toBe('system');
  });

  it('showLabel=true 时显示文字', () => {
    render(<ThemeToggle showLabel />);
    expect(screen.getByText('跟随系统')).toBeInTheDocument();
  });

  it('挂载时读取 localStorage 中已存在的 mode', () => {
    localStorage.setItem('ic.theme', 'dark');
    render(<ThemeToggle />);
    const btn = screen.getByTestId('theme-toggle');
    expect(btn.getAttribute('data-theme-mode')).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('className 注入有效', () => {
    render(<ThemeToggle className="custom-class" />);
    const btn = screen.getByTestId('theme-toggle');
    expect(btn.className).toMatch(/custom-class/);
  });
});
