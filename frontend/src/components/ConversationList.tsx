/**
 * v2.2 PR4 — 多会话历史侧栏 / 列表组件。
 *
 * 用途：
 *   - 桌面端作为常驻侧栏（左侧 280px）；
 *   - 移动端嵌入 Drawer（由调用方控制）。
 *
 * 功能：
 *   - 新建会话按钮（顶部）；
 *   - 会话列表（按 updatedAt 倒序）；
 *   - 当前会话高亮；
 *   - hover 显示删除按钮；
 *   - 空态提示（EmptyState noData）。
 *
 * 数据来源：useConversations hook。
 */

import { useConversations, type StoredConversation } from '../hooks/useConversations';
import { EmptyState } from './EmptyState';

export interface ConversationListProps {
  /** 选中后是否需要调用方做后续动作（如关闭 Drawer） */
  onSelect?: (id: string) => void;
  /** 删除会话后通知调用方清理对应的 messages / serverConversationId / localStorage */
  onDelete?: (id: string) => void;
}

function relativeTime(ts: number): string {
  const now = Date.now();
  const diff = now - ts;
  if (diff < 60_000) return '刚刚';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`;
  return new Date(ts).toLocaleDateString('zh-CN');
}

export function ConversationList({ onSelect, onDelete }: ConversationListProps) {
  const conv = useConversations();
  const { list, currentId, createNew, setCurrentId, remove } = conv;

  function handleNew() {
    createNew();
  }

  function handleSelect(id: string) {
    setCurrentId(id);
    onSelect?.(id);
  }

  function handleDelete(e: React.MouseEvent, id: string) {
    e.stopPropagation();
    if (typeof window !== 'undefined' && !window.confirm('确定删除该会话吗？')) return;
    remove(id);
    // Notify the parent so it can clear messages / serverConversationId /
    // localStorage ic.conversation_id — otherwise the chat pane keeps
    // showing the deleted session's history and the user thinks delete
    // didn't work.
    onDelete?.(id);
  }

  return (
    <aside
      aria-label="历史会话"
      className="flex flex-col h-full bg-app-surface border-r border-app-border"
      data-testid="conversation-list"
    >
      <div className="p-3 border-b border-app-border">
        <button
          type="button"
          onClick={handleNew}
          aria-label="新建会话"
          data-testid="conversation-new"
          className="w-full bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 py-2 rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg"
        >
          ＋ 新建会话
        </button>
      </div>
      <div className="flex-1 overflow-y-auto py-2">
        {list.length === 0 ? (
          <div className="px-3 py-4">
            <EmptyState
              variant="noData"
              icon="💬"
              title="暂无历史会话"
              description="点击上方按钮开始一个新对话。"
            />
          </div>
        ) : (
          <ul className="flex flex-col gap-1 px-2" role="list">
            {list.map((c) => (
              <ConversationRow
                key={c.id}
                c={c}
                active={c.id === currentId}
                onSelect={handleSelect}
                onDelete={handleDelete}
              />
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}

interface RowProps {
  c: StoredConversation;
  active: boolean;
  onSelect: (id: string) => void;
  onDelete: (e: React.MouseEvent, id: string) => void;
}

function ConversationRow({ c, active, onSelect, onDelete }: RowProps) {
  return (
    <li
      className={
        'group relative rounded-lg transition-colors border-l-[3px] ' +
        (active
          ? 'bg-brand-50 dark:bg-brand-900/30 border-app-brand'
          : 'border-transparent hover:bg-app-surface-muted')
      }
      data-testid={`conversation-row-${c.id}`}
      aria-current={active ? 'true' : undefined}
    >
      <button
        type="button"
        onClick={() => onSelect(c.id)}
        className="w-full text-left rounded-lg px-3 py-2 pr-9 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
      >
        <div className="min-w-0 flex-1">
          <div
            className={
              'text-sm font-medium truncate ' +
              (active ? 'text-brand-700 dark:text-brand-300' : 'text-app-text')
            }
          >
            {c.title || '新会话'}
          </div>
          <div className="text-[11px] text-app-text-muted mt-0.5 truncate">
            {c.preview || '（暂无内容）'}
          </div>
          <div className="text-[10px] text-app-text-muted mt-1">
            {relativeTime(c.updatedAt)}
          </div>
        </div>
      </button>
      <button
        type="button"
        onClick={(e) => onDelete(e, c.id)}
        aria-label={`删除会话：${c.title}`}
        className="absolute right-1 top-1 opacity-0 group-hover:opacity-100 focus:opacity-100 text-app-text-muted hover:text-danger-600 transition-opacity p-1 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand"
      >
        🗑
      </button>
    </li>
  );
}

export default ConversationList;
