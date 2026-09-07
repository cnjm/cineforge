import { X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { DashboardSummary, Task, UserNotification } from "../types";
import { isOverdueTask } from "../utils";

type NotificationTab = "updates" | "work";

type NotificationModalProps = {
  tasks: Task[];
  dashboard: DashboardSummary | null;
  notifications: UserNotification[];
  onClose: () => void;
};

export function NotificationModal({
  tasks,
  dashboard,
  notifications,
  onClose,
}: NotificationModalProps) {
  const [tab, setTab] = useState<NotificationTab>("updates");
  const [entered, setEntered] = useState(false);

  useEffect(() => {
    const frame = requestAnimationFrame(() => setEntered(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const overdueTasks = tasks.filter(isOverdueTask);

  // TODO(backend): 工作提醒 Tab 数据目前由前端拼装：
  //   1) GET /api/workspace/notifications 返回的业务通知（已实现）
  //   2) 前端根据任务计算的逾期提醒（临时方案，后端应提供逾期预警接口或定时任务）
  //   3) 未来应新增通知分类（tab 字段）、更多触发场景的写入逻辑
  const workItems = useMemo(() => {
    const fromApi = notifications.map((item) => ({
      id: String(item.id),
      title: String(item.title || "通知"),
      badge: notificationLabel(String(item.type || "")),
      content: String(item.content || ""),
      created_at: String(item.created_at || ""),
      unread: !item.is_read,
    }));

    const overdue = overdueTasks.map((task) => ({
      id: `overdue-${task.id}`,
      title: task.title,
      badge: "逾期",
      content: `当前状态：${task.status}\n截止时间：${formatNotificationTime(task.due_at)}`,
      created_at: task.due_at || new Date().toISOString(),
      unread: true,
    }));

    return [...fromApi, ...overdue];
  }, [notifications, overdueTasks]);

  // TODO(backend): 更新推送 Tab 目前依赖 dashboard.recent_activity（硬编码字符串），
  //   后端应提供系统通知/版本发布接口，或在 notifications 表中增加 tab 字段区分分类。
  const updateItems = useMemo(() => {
    const activities = dashboard?.recent_activity ?? [];
    if (!activities.length) return [];
    return activities.map((item, index) => ({
      id: `activity-${index}`,
      title: "系统动态",
      badge: "系统",
      content: item,
      created_at: new Date().toISOString(),
      unread: false,
    }));
  }, [dashboard]);

  const workUnreadCount = workItems.filter((item) => item.unread).length;
  const updateUnreadCount = updateItems.filter((item) => item.unread).length;

  const visibleItems = tab === "updates" ? updateItems : workItems;

  function handleClose() {
    setEntered(false);
    window.setTimeout(onClose, 220);
  }

  return (
    <div className={`notify-center ${entered ? "is-open" : ""}`} role="dialog" aria-modal="true" aria-label="通知中心">
      <button type="button" className="notify-center__backdrop" aria-label="关闭通知中心" onClick={handleClose} />
      <aside className="notify-center__panel" onClick={(event) => event.stopPropagation()}>
        <header className="notify-center__header">
          <h2 className="notify-center__title">通知中心</h2>
          <button type="button" className="notify-center__close" onClick={handleClose} aria-label="关闭">
            <X size={18} strokeWidth={1.8} />
          </button>
        </header>

        <div className="notify-center__tabs" role="tablist" aria-label="通知分类">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "updates"}
            className={`notify-center__tab ${tab === "updates" ? "is-active" : ""}`}
            onClick={() => setTab("updates")}
          >
            更新推送
            {updateUnreadCount > 0 ? <i className="notify-center__tab-dot" aria-hidden="true" /> : null}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "work"}
            className={`notify-center__tab ${tab === "work" ? "is-active" : ""}`}
            onClick={() => setTab("work")}
          >
            工作提醒
            {workUnreadCount > 0 ? <i className="notify-center__tab-dot" aria-hidden="true" /> : null}
          </button>
        </div>

        <div className="notify-center__list" role="tabpanel">
          {visibleItems.map((item) => (
            <article className={`notify-center__item ${item.unread ? "is-unread" : ""}`} key={item.id}>
              <div className="notify-center__item-head">
                <h3 className="notify-center__item-title">
                  {item.unread ? <i className="notify-center__unread-dot" aria-hidden="true" /> : null}
                  <span>{item.title}</span>
                </h3>
                <span className="notify-center__badge">{item.badge}</span>
              </div>
              <p className="notify-center__item-body">{renderRichText(item.content)}</p>
              <time className="notify-center__item-time" dateTime={item.created_at}>
                {formatNotificationTime(item.created_at)}
              </time>
            </article>
          ))}
          {!visibleItems.length ? <div className="notify-center__empty">暂无通知</div> : null}
        </div>
      </aside>
    </div>
  );
}

function notificationLabel(type: string) {
  return (
    (
      {
        task_reassigned_out: "改派",
        task_reassigned_in: "改派",
        task_rework: "返工",
        task_approved: "定版",
        asset_unlocked: "资产",
        // TODO(backend): 以下通知类型的 badge 映射待后端实现后补充
        // task_submitted: "提交",
        // storyboard_review: "审批",
        // member_updated: "成员",
        // asset_confirmed: "资产",
        // script_completed: "剧本",
        // progress_reminder: "进度",
        // task_overdue: "逾期",
      } as Record<string, string>
    )[type] || "通知"
  );
}

function formatNotificationTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  const ss = String(date.getSeconds()).padStart(2, "0");
  return `${y}-${m}-${d} ${hh}:${mm}:${ss}`;
}

/** Unread count for the bell badge, driven purely by live backend data. */
export function getNotificationBadgeCount(notifications: UserNotification[], overdueCount = 0): number {
  // TODO(backend): 当前角标仅使用未读通知 + 前端计算的逾期数。
  //   未来应改为从后端获取统一的未读计数接口，避免前端拼装逻辑。
  return notifications.filter((item) => !item.is_read).length + overdueCount;
}

function renderRichText(text: string) {
  const lines = text.split("\n");
  const nodes: ReactNode[] = [];
  lines.forEach((line, lineIndex) => {
    const parts = line.split(/(https?:\/\/[^\s]+)/g);
    parts.forEach((part, index) => {
      if (/^https?:\/\//.test(part)) {
        nodes.push(
          <a key={`l${lineIndex}-${part}-${index}`} href={part} target="_blank" rel="noreferrer" className="notify-center__link">
            {part}
          </a>
        );
      } else if (part) {
        nodes.push(<span key={`l${lineIndex}-${index}-${part.slice(0, 8)}`}>{part}</span>);
      }
    });
    if (lineIndex < lines.length - 1) {
      nodes.push(<br key={`br-${lineIndex}`} />);
    }
  });
  return nodes;
}
