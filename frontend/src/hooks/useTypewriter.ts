/**
 * v2.2 PR4 — 流式打字机 hook。
 *
 * 用途：
 *   - 把目标字符串（agent 完整回复 / 中间流式拼接结果）按"逐 token"逐步显示。
 *   - 每 token 间隔 speedMs（默认 20ms ≤ PRD §9.2 "≤ 30ms" 上限）。
 *   - 调用 skip() 立即显示全文（不会取消后续流）。
 *   - 调用 reset() 清空（下次 text 变化时重新开始）。
 *
 * 与 streamChat 的协作：
 *   - 调用方在 onStreamEvent 拼接 final.content 或 step.content 时，把累计文本传给 hook 的 text。
 *   - final 事件到达时一次性 setText(finalContent)；打字机会快速走完。
 *   - 用户 stop / 中断时，保留已打字内容（不调用 reset）。
 *
 * 实现细节：
 *   - 使用 setInterval（fake-timer 友好）+ 时间阈值。
 *   - 当 text 缩短（reset 路径），立即清空；不缩短时只增不删。
 *   - 到达 text.length 时 clearInterval + setDone(true)。
 *   - SSR 兼容：typeof window 守卫。
 */

import { useCallback, useEffect, useRef, useState } from 'react';

export interface UseTypewriterReturn {
  /** 当前已显示的字符串 */
  displayed: string;
  /** 是否打字完毕（displayed === text） */
  done: boolean;
  /** 立即跳过打字机（一次性显示完整 text） */
  skip: () => void;
  /** 重置（清空 displayed）；下次 text 变化时重新开始 */
  reset: () => void;
}

export interface UseTypewriterOptions {
  /** 每 token 间隔（毫秒），默认 20ms（PRD §9.2 上限 30ms） */
  speedMs?: number;
  /** 初始是否禁用（默认 false）；true 时直接显示完整 text */
  disabled?: boolean;
}

export function useTypewriter(
  text: string,
  options: UseTypewriterOptions = {},
): UseTypewriterReturn {
  const { speedMs = 20, disabled = false } = options;
  const [displayed, setDisplayed] = useState<string>(disabled ? text : '');
  const [done, setDone] = useState<boolean>(disabled);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const cancelledRef = useRef<boolean>(false);
  // 持久化当前 text 引用，避免 useEffect 中闭包陈旧
  const textRef = useRef<string>(text);

  // 同步更新 textRef
  useEffect(() => {
    textRef.current = text;
  }, [text]);

  const clearTimer = useCallback(() => {
    if (intervalRef.current !== null) {
      clearInterval(intervalRef.current);
      intervalRef.current = null;
    }
  }, []);

  // 同步 skip / reset
  const skip = useCallback(() => {
    cancelledRef.current = true;
    clearTimer();
    setDisplayed(textRef.current);
    setDone(true);
  }, [clearTimer]);

  const reset = useCallback(() => {
    cancelledRef.current = true;
    clearTimer();
    setDisplayed('');
    setDone(false);
  }, [clearTimer]);

  useEffect(() => {
    // disabled：直接同步
    if (disabled) {
      setDisplayed(text);
      setDone(true);
      return;
    }

    // 启动打字机
    cancelledRef.current = false;
    clearTimer();

    intervalRef.current = setInterval(() => {
      if (cancelledRef.current) {
        clearTimer();
        return;
      }
      const current = textRef.current;
      setDisplayed((prev) => {
        if (prev.length >= current.length) {
          // 到达末尾
          clearTimer();
          setDone(true);
          return current;
        }
        return current.slice(0, prev.length + 1);
      });
    }, speedMs);

    return () => {
      cancelledRef.current = true;
      clearTimer();
    };
  }, [text, speedMs, disabled, clearTimer]);

  // 监听 done 的真实状态（当 displayed 追上 text 时设置 done）
  useEffect(() => {
    if (displayed.length >= text.length && !done) {
      setDone(true);
    } else if (displayed.length < text.length && done && text.length > 0) {
      // text 变长但还没打完
      setDone(false);
    }
  }, [displayed, text, done]);

  return { displayed, done, skip, reset };
}

export default useTypewriter;
