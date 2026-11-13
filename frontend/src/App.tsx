import { Suspense, useEffect, useRef, useState } from "react";
import { BrowserRouter as Router } from "react-router-dom";
import { Layout } from "./shared/layout";
import { ToastContainer, Modal, Button, Loading, useToastStore } from "./shared/components";
import {
  AlertTriangle,
  ChevronRight,
  Loader2,
  LogOut,
  Minimize2,
  Power,
} from "lucide-react";
import { AppRoutes } from "./routes/AppRoutes";
import { lazyNamed } from "./routes/lazyNamed";
import { useNotificationStore } from "./store/notificationStore";
import { useBackupStore } from "./store/backupStore";
import type { BrowserInputSyncStatus } from "./modules/browser/types";
import { installWailsOperationLogger } from "./utils/wailsOperationLogger";
import {
  ForceQuit as ForceQuitApp,
  QuitAppOnly as QuitAppOnlyApp,
} from "./wailsjs/go/main/App";
import {
  Environment,
  Quit,
  WindowHide,
  WindowMinimise,
  InitializeNotifications,
  IsNotificationAvailable,
  RequestNotificationAuthorization,
  CheckNotificationAuthorization,
  SendNotification,
} from "./wailsjs/runtime/runtime";

const QuickLaunchModal = lazyNamed(
  () => import("./modules/browser/components/QuickLaunchModal"),
  "QuickLaunchModal",
);

function useWailsNotifications() {
  const addNotification = useNotificationStore((s) => s.addNotification);

  useEffect(() => {
    const runtime = (window as any).runtime;
    if (!runtime?.EventsOn) return;

    const offCrashed = runtime.EventsOn(
      "browser:instance:crashed",
      (data: { profileId: string; profileName: string; error: string }) => {
        addNotification({
          type: "error",
          title: "实例异常退出",
          message: `「${data.profileName || data.profileId}」意外崩溃：${data.error}`,
        });
      },
    );

    return () => {
      offCrashed?.();
    };
  }, [addNotification]);
}

function useInputSyncNotifications() {
  const addNotification = useNotificationStore((s) => s.addNotification);
  const nativeReadyRef = useRef(false);
  const previousStatusRef = useRef<BrowserInputSyncStatus | null>(null);
  const notifiedKeysRef = useRef<Set<string>>(new Set());
  const activePauseKeysRef = useRef<Map<string, string>>(new Map());

  useEffect(() => {
    let cancelled = false;
    async function initNativeNotifications() {
      try {
        const available = await IsNotificationAvailable();
        if (!available || cancelled) return;
        await InitializeNotifications();
        let authorized = await CheckNotificationAuthorization();
        if (!authorized) {
          authorized = await RequestNotificationAuthorization();
        }
        if (!cancelled) nativeReadyRef.current = authorized;
      } catch {
        if (!cancelled) nativeReadyRef.current = false;
      }
    }
    void initNativeNotifications();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const runtime = (window as any).runtime;
    if (!runtime?.EventsOn) return;

    const sendNotice = (
      type: "info" | "success" | "warning" | "error",
      title: string,
      message: string,
      key: string,
    ) => {
      if (notifiedKeysRef.current.has(key)) return;
      notifiedKeysRef.current.add(key);
      addNotification({ type, title, message });
      useToastStore.getState().addToast({
        type,
        message: `${title}\n${message}`,
        duration: type === "success" || type === "info" ? 5000 : 7000,
      });
      if (nativeReadyRef.current) {
        void SendNotification({
          id: key.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 96),
          title,
          body: message,
        }).catch(() => {
          nativeReadyRef.current = false;
        });
      }
    };

    const off = runtime.EventsOn("browser:input-sync:status", (status: BrowserInputSyncStatus) => {
      const previous = previousStatusRef.current;
      const sessionKey = status?.sessionId || "session";
      const previousSessionKey = previous?.sessionId || "session";
      if (status?.running && previous?.running && sessionKey !== previousSessionKey) {
        notifiedKeysRef.current.clear();
        activePauseKeysRef.current.clear();
      }
      previousStatusRef.current = status;

      if (!status?.running) {
        notifiedKeysRef.current.clear();
        activePauseKeysRef.current.clear();
        return;
      }

      if (status.warning) {
        sendNotice(
          "error",
          "输入同步异常",
          status.warning,
          `input-sync-session-warning:${status.sessionId || "session"}:${status.warning}`,
        );
      }

      const previousTargets = new Map((previous?.targets || []).map(target => [target.profileId, target]));
      for (const target of status.targets || []) {
        const previousTarget = previousTargets.get(target.profileId);
        const name = target.profileName || target.profileId;
        const reason = target.pauseReason || target.warning || "输入同步已暂停";
        const targetKey = `${sessionKey}:${target.profileId}`;
        if (target.paused && (!previousTarget?.paused || previousTarget.pauseReason !== target.pauseReason || previousTarget.warning !== target.warning)) {
          const pauseKey = `input-sync-paused:${targetKey}:${target.pauseKind || "unknown"}:${reason}`;
          sendNotice(
            target.recoverable ? "warning" : "error",
            "输入同步目标已暂停",
            `${name}：${reason}`,
            pauseKey,
          );
          activePauseKeysRef.current.set(targetKey, pauseKey);
          continue;
        }
        if (!target.paused && previousTarget?.paused && target.ready) {
          const pauseKey = activePauseKeysRef.current.get(targetKey);
          if (pauseKey) {
            sendNotice(
              "success",
              "输入同步目标已恢复",
              `${name} 已恢复同步`,
              `input-sync-recovered:${targetKey}:${pauseKey}`,
            );
          }
          activePauseKeysRef.current.delete(targetKey);
          continue;
        }
        if (!target.paused && activePauseKeysRef.current.has(targetKey) && !previousTarget?.paused) {
          activePauseKeysRef.current.delete(targetKey);
        }
      }
    });

    return () => {
      off?.();
    };
  }, [addNotification]);
}

function useGlobalErrorNotifications() {
  const addNotification = useNotificationStore((s) => s.addNotification);

  useEffect(() => {
    const toMessage = (value: unknown) => {
      if (value instanceof Error) return value.message || String(value);
      if (typeof value === "string") return value;
      try {
        return JSON.stringify(value);
      } catch {
        return String(value);
      }
    };

    const handleError = (event: ErrorEvent) => {
      addNotification({
        type: "error",
        title: "前端异常",
        message: event.message || toMessage(event.error) || "未知脚本错误",
      });
    };

    const handleUnhandledRejection = (event: PromiseRejectionEvent) => {
      addNotification({
        type: "error",
        title: "未处理异步异常",
        message: toMessage(event.reason) || "未知 Promise 异常",
      });
    };

    window.addEventListener("error", handleError);
    window.addEventListener("unhandledrejection", handleUnhandledRejection);
    return () => {
      window.removeEventListener("error", handleError);
      window.removeEventListener("unhandledrejection", handleUnhandledRejection);
    };
  }, [addNotification]);
}

function CloseConfirmModal() {
  const [open, setOpen] = useState(false);
  const [platform, setPlatform] = useState("windows");
  const [quittingAction, setQuittingAction] = useState<
    "app-only" | "app-and-browser" | null
  >(null);
  const importInProgress = useBackupStore((s) => s.importInProgress);
  const importProgress = useBackupStore((s) => s.importProgress);
  const importMessage = useBackupStore((s) => s.importMessage);
  const supportsTray = platform === "windows";
  const quitting = quittingAction !== null;

  useEffect(() => {
    const runtime = (window as any).runtime;
    if (!runtime?.EventsOn) return;

    const off = runtime.EventsOn("app:request-close", () => {
      setQuittingAction(null);
      setOpen(true);
    });
    return () => {
      if (typeof off === "function") off();
    };
  }, []);

  useEffect(() => {
    let cancelled = false;

    Environment()
      .then((info) => {
        if (!cancelled && info?.platform) {
          setPlatform(info.platform);
        }
      })
      .catch(() => {});

    return () => {
      cancelled = true;
    };
  }, []);

  const closeModal = () => {
    if (quitting) return;
    setOpen(false);
  };

  const handleMinimize = () => {
    if (quitting) return;
    setOpen(false);
    if (supportsTray) {
      WindowHide();
      return;
    }
    WindowMinimise();
  };

  const handleQuitAppOnly = async () => {
    setQuittingAction("app-only");
    try {
      await QuitAppOnlyApp();
    } catch (error) {
      console.error("QuitAppOnly failed", error);
      setQuittingAction(null);
    }
  };

  const handleQuitAppAndBrowsers = async () => {
    setQuittingAction("app-and-browser");
    try {
      await Promise.race([
        ForceQuitApp(),
        new Promise((resolve) => setTimeout(resolve, 1200)),
      ]);
    } catch (error) {
      console.error("ForceQuit failed, falling back to runtime.Quit()", error);
    }
    Quit();
  };

  return (
    <Modal
      open={open}
      onClose={closeModal}
      title={importInProgress ? "关闭应用" : "退出 xFacade"}
      width="440px"
      closable={!quitting}
    >
      {importInProgress ? (
        <div className="space-y-4">
          <div className="flex items-start gap-3 rounded-[10px] border border-[rgb(245_165_36_/_0.22)] bg-[rgb(245_165_36_/_0.07)] p-4">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[rgb(245_165_36_/_0.12)] text-[var(--color-warning)]">
              <AlertTriangle className="h-5 w-5" />
            </div>
            <div className="min-w-0">
              <h4 className="text-[13.5px] font-semibold text-[var(--color-text-primary)]">
                配置正在加载{importProgress > 0 ? `（${importProgress}%）` : ""}
              </h4>
              <p className="mt-1 text-xs leading-5 text-[var(--color-text-muted)]">
                {importMessage || "此时关闭会中断本次加载，并可能导致部分配置未完成写入。"}
              </p>
            </div>
          </div>
          <div className="flex w-full gap-2.5">
            <Button
              variant="secondary"
              className="flex-1"
              onClick={closeModal}
              disabled={quitting}
            >
              继续加载
            </Button>
            <Button
              variant="danger"
              className="flex-1"
              onClick={handleQuitAppAndBrowsers}
              loading={quittingAction === "app-and-browser"}
            >
              仍要关闭
            </Button>
          </div>
        </div>
      ) : (
        <div className="space-y-2.5">
          {supportsTray && (
            <button
              type="button"
              onClick={handleMinimize}
              disabled={quitting}
              className="group flex min-h-[68px] w-full items-center gap-3 rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-3.5 py-3 text-left transition-all hover:border-[var(--color-border-strong)] hover:bg-[var(--color-bg-subtle)] disabled:cursor-not-allowed disabled:opacity-50"
            >
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--color-accent-muted)] text-[var(--color-accent)]">
                <Minimize2 className="h-[18px] w-[18px]" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[13.5px] font-semibold text-[var(--color-text-primary)]">最小化至托盘</div>
                <div className="mt-1 text-xs text-[var(--color-text-muted)]">隐藏窗口，应用在后台继续运行</div>
              </div>
              <ChevronRight className="h-4 w-4 shrink-0 text-[var(--color-text-muted)] transition-transform group-hover:translate-x-0.5" />
            </button>
          )}

          <button
            type="button"
            onClick={handleQuitAppOnly}
            disabled={quitting}
            className="group flex min-h-[68px] w-full items-center gap-3 rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-3.5 py-3 text-left transition-all hover:border-[var(--color-border-strong)] hover:bg-[var(--color-bg-subtle)] disabled:cursor-not-allowed disabled:opacity-50"
          >
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[rgb(245_165_36_/_0.1)] text-[var(--color-warning)]">
              <LogOut className="h-[18px] w-[18px]" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[13.5px] font-semibold text-[var(--color-text-primary)]">仅退出客户端</div>
              <div className="mt-1 text-xs text-[var(--color-text-muted)]">关闭客户端，保留已打开的浏览器</div>
            </div>
            {quittingAction === "app-only" ? (
              <Loader2 className="h-4 w-4 shrink-0 animate-spin text-[var(--color-accent)]" />
            ) : (
              <ChevronRight className="h-4 w-4 shrink-0 text-[var(--color-text-muted)] transition-transform group-hover:translate-x-0.5" />
            )}
          </button>

          <button
            type="button"
            onClick={handleQuitAppAndBrowsers}
            disabled={quitting}
            className="group flex min-h-[68px] w-full items-center gap-3 rounded-[10px] border border-[rgb(239_71_87_/_0.2)] bg-[rgb(239_71_87_/_0.045)] px-3.5 py-3 text-left transition-all hover:border-[rgb(239_71_87_/_0.34)] hover:bg-[rgb(239_71_87_/_0.075)] disabled:cursor-not-allowed disabled:opacity-50"
          >
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[rgb(239_71_87_/_0.12)] text-[var(--color-error)]">
              <Power className="h-[18px] w-[18px]" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[13.5px] font-semibold text-[var(--color-error)]">退出全部进程</div>
              <div className="mt-1 text-xs text-[var(--color-text-muted)]">关闭客户端，终止所有浏览器实例</div>
            </div>
            {quittingAction === "app-and-browser" ? (
              <Loader2 className="h-4 w-4 shrink-0 animate-spin text-[var(--color-error)]" />
            ) : (
              <ChevronRight className="h-4 w-4 shrink-0 text-[var(--color-error)] opacity-60 transition-transform group-hover:translate-x-0.5" />
            )}
          </button>
        </div>
      )}
    </Modal>
  );
}

function App() {
  useEffect(() => {
    installWailsOperationLogger();
  }, []);
  useWailsNotifications();
  useInputSyncNotifications();
  useGlobalErrorNotifications();
  const [quickLaunchOpen, setQuickLaunchOpen] = useState(false);
  const routeFallback = (
    <div className="flex min-h-[240px] items-center justify-center py-10">
      <Loading text="页面加载中..." />
    </div>
  );

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.isComposing) return;
      if (!(event.ctrlKey || event.metaKey)) return;
      if (event.key.toLowerCase() !== "k") return;
      event.preventDefault();
      setQuickLaunchOpen((prev) => !prev);
    };

    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  return (
    <Router>
      <Layout>
        <Suspense fallback={routeFallback}>
          <AppRoutes />
        </Suspense>
      </Layout>
      <ToastContainer />
      <CloseConfirmModal />
      <Suspense fallback={null}>
        {quickLaunchOpen ? (
          <QuickLaunchModal
            open={quickLaunchOpen}
            onClose={() => setQuickLaunchOpen(false)}
          />
        ) : null}
      </Suspense>
    </Router>
  );
}

export default App;
