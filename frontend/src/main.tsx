import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import App from './App';
import AdminApp from './admin/AdminApp';
import { ErrorBoundary } from './components/ErrorBoundary';
import './index.css';

// v2.2 PR1：全局 ErrorBoundary 包裹整个 <App /> 和 <AdminApp />。
// 任意子组件渲染期抛错都会被捕获，避免白屏；显示统一降级页 + 错误编号。
//
// 三层结构：
//   <ErrorBoundary scope="global">      ← 全局兜底（任何路由崩了）
//     <BrowserRouter>
//       <Routes>...</Routes>
//     </BrowserRouter>
//   </ErrorBoundary>
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary scope="global">
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<App />} />
          <Route path="/admin/*" element={<AdminApp />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </ErrorBoundary>
  </React.StrictMode>,
);