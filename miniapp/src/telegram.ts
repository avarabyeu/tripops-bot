/**
 * Thin, typed access to the Telegram WebApp SDK.
 *
 * Everything here degrades gracefully when the SDK is absent, because the app
 * is also opened in a plain browser during development. Nothing else in the
 * codebase touches `window.Telegram`.
 */

export interface TelegramUser {
  id: number;
  first_name: string;
  last_name?: string;
  username?: string;
  photo_url?: string;
}

interface BackButton {
  show(): void;
  hide(): void;
  onClick(cb: () => void): void;
  offClick(cb: () => void): void;
}

interface MainButton {
  text: string;
  isVisible: boolean;
  show(): void;
  hide(): void;
  setText(text: string): void;
  onClick(cb: () => void): void;
  offClick(cb: () => void): void;
  showProgress(leaveActive?: boolean): void;
  hideProgress(): void;
  enable(): void;
  disable(): void;
}

interface HapticFeedback {
  impactOccurred(style: "light" | "medium" | "heavy"): void;
  notificationOccurred(type: "error" | "success" | "warning"): void;
  selectionChanged(): void;
}

interface WebApp {
  initData: string;
  initDataUnsafe: { user?: TelegramUser; start_param?: string };
  version: string;
  colorScheme: "light" | "dark";
  themeParams: Record<string, string>;
  isExpanded: boolean;
  ready(): void;
  expand(): void;
  close(): void;
  BackButton: BackButton;
  MainButton: MainButton;
  HapticFeedback: HapticFeedback;
  showAlert(message: string, cb?: () => void): void;
  showConfirm(message: string, cb: (ok: boolean) => void): void;
  openLink(url: string): void;
  openTelegramLink(url: string): void;
  setHeaderColor?(color: string): void;
  disableVerticalSwipes?(): void;
}

declare global {
  interface Window {
    Telegram?: { WebApp: WebApp };
  }
}

export const webApp = (): WebApp | undefined => window.Telegram?.WebApp;

/** True when running inside Telegram rather than a desktop browser. */
export const insideTelegram = (): boolean => Boolean(webApp()?.initData);

/**
 * The signed launch parameters. Every API call carries them; the backend
 * re-verifies the signature on each request, so this is a credential, not a
 * hint.
 */
export const initData = (): string => webApp()?.initData ?? "";

/** The deep link payload, e.g. `trip_<id>` or `inv_<token>`. */
export const startParam = (): string | undefined =>
  webApp()?.initDataUnsafe?.start_param;

export function ready(): void {
  const app = webApp();
  if (!app) return;
  app.ready();
  app.expand();
  // A trip screen is a scrolling list; the swipe-to-close gesture fights it.
  app.disableVerticalSwipes?.();
}

/** Drives the native back button from the router. */
export function setBackButton(visible: boolean, onBack: () => void): () => void {
  const app = webApp();
  if (!app) return () => {};
  const button = app.BackButton;
  if (!visible) {
    button.hide();
    return () => {};
  }
  button.onClick(onBack);
  button.show();
  return () => {
    button.offClick(onBack);
    button.hide();
  };
}

export const haptic = {
  tap(): void {
    webApp()?.HapticFeedback.selectionChanged();
  },
  success(): void {
    webApp()?.HapticFeedback.notificationOccurred("success");
  },
  error(): void {
    webApp()?.HapticFeedback.notificationOccurred("error");
  },
};

/** Telegram's own alert, falling back to the browser's. */
export function alert(message: string): void {
  const app = webApp();
  if (app) app.showAlert(message);
  else window.alert(message);
}

export function confirm(message: string): Promise<boolean> {
  const app = webApp();
  if (!app) return Promise.resolve(window.confirm(message));
  return new Promise((resolve) => app.showConfirm(message, resolve));
}

/** Opens a link the way Telegram expects, so t.me links stay in the client. */
export function openLink(url: string): void {
  const app = webApp();
  if (!app) {
    window.open(url, "_blank", "noopener");
    return;
  }
  if (url.includes("t.me/")) app.openTelegramLink(url);
  else app.openLink(url);
}

export function share(url: string, text: string): void {
  openLink(
    `https://t.me/share/url?url=${encodeURIComponent(url)}&text=${encodeURIComponent(text)}`,
  );
}
