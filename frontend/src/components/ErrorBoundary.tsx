import React from 'react';

/**
 * ErrorBoundary — React 错误边界组件（v2.2 PR1）。
 *
 * 用途：
 *   - 全局级（main.tsx）：兜底整个应用；崩了显示完整降级页 + 错误编号。
 *   - 路由级（App.tsx / admin/AdminApp.tsx）：崩了保留导航条，仅主区降级。
 *
 * 设计要点：
 *   1. fallback prop 允许调用方自定义降级 UI；缺省用全局降级页。
 *   2. 错误编号格式：`ERR-YYYYMMDD-HHMMSS-<随机串>`，便于用户反馈时引用。
 *   3. componentDidCatch 同时调用 console.error（开发期可见）+ 不外发上报
 *      （v2.2.1 接入 Sentry 时再加）。
 *
 * 注意：
 *   - ErrorBoundary 必须用 class component（React API 限制）；function + hook
 *     不能捕获渲染期错误。
 *   - ErrorBoundary 不能捕获自身错误，也不能捕获异步代码 / event handler 错误。
 */
interface Props {
  fallback?: React.ReactNode;
  /** 可选：路由名（用于错误编号前缀，区分 user / admin）。 */
  scope?: 'user' | 'admin' | 'global';
  children: React.ReactNode;
}

interface State {
  hasError: boolean;
  error?: Error;
  errorId?: string;
}

export class ErrorBoundary extends React.Component<Props, State> {
  state: State = { hasError: false };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    // 生成错误编号：ERR-<scope>-<YYYYMMDD>-<HHMMSS>-<random4>
    const now = new Date();
    const ymd =
      now.getFullYear().toString() +
      String(now.getMonth() + 1).padStart(2, '0') +
      String(now.getDate()).padStart(2, '0');
    const hms =
      String(now.getHours()).padStart(2, '0') +
      String(now.getMinutes()).padStart(2, '0') +
      String(now.getSeconds()).padStart(2, '0');
    const rand = Math.random().toString(36).slice(2, 6).toUpperCase();
    const scope = this.props.scope ?? 'global';
    const errorId = `ERR-${scope}-${ymd}-${hms}-${rand}`;
    this.setState({ errorId });
    // 开发期直接打到 console；生产期由日志聚合工具收集。
    // eslint-disable-next-line no-console
    console.error('[ErrorBoundary]', error, info, { errorId });
  }

  private handleRefresh = () => {
    if (typeof window !== 'undefined') {
      window.location.reload();
    }
  };

  render() {
    if (!this.state.hasError) {
      return this.props.children;
    }
    if (this.props.fallback !== undefined) {
      return this.props.fallback;
    }
    // 全局默认降级 UI。
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-50 p-8">
        <div className="max-w-md text-center">
          <div className="text-amber-500 text-4xl mb-3" aria-hidden="true">
            ⚠
          </div>
          <h1 className="text-xl font-semibold text-slate-800 mb-2">页面出错了</h1>
          <p className="text-sm text-slate-600 mb-4">
            我们已记录此错误，请刷新页面或联系管理员。
          </p>
          <button
            type="button"
            onClick={this.handleRefresh}
            className="bg-brand-500 hover:bg-brand-600 text-white text-sm px-4 py-2 rounded-lg focus:outline-none focus:ring-2 focus:ring-brand-500/40"
          >
            刷新页面
          </button>
          {this.state.errorId && (
            <p className="mt-4 text-xs text-slate-400 font-mono">
              错误编号：{this.state.errorId}
            </p>
          )}
        </div>
      </div>
    );
  }
}

export default ErrorBoundary;