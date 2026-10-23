/**
 * Idle sign-out logic, free of Angular and the DOM so it can be tested with a fake clock.
 *
 * "Idle" means no user activity (the caller reports pointer, key, touch and scroll events) in any
 * tab of this browser: the time of the last activity is shared through `env.read`/`env.write`
 * (localStorage in the app). A request the app makes on its own is not activity.
 */

export interface IdleEnv {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
  /** Last activity in any tab (ms since the epoch), or null when unknown or storage is unavailable. */
  read(): number | null;
  write(ms: number): void;
}

export interface IdleHandlers {
  /** Fires once when the warning period starts. */
  warn(msLeft: number): void;
  /** Fires about once a second while the warning is up. */
  tick(msLeft: number): void;
  /** The warning is over because the user stayed (here or in another tab). */
  resume(): void;
  /** The timeout has passed. The timer has stopped itself. */
  expire(): void;
}

/** Activity is written to shared storage at most this often. */
const WRITE_EVERY_MS = 2_000;
/** Look at shared storage at least this often while no warning is up. */
const MAX_WAIT_MS = 15_000;
const MIN_WAIT_MS = 250;

/** The warning lasts up to a minute, but never more than half the timeout. */
export function warnWindowMs(timeoutMs: number): number {
  return Math.min(60_000, Math.floor(timeoutMs / 2));
}

export class IdleTimer {
  private readonly warnMs: number;
  private last = 0;
  private lastWrite = 0;
  private handle: unknown = null;
  private warned = false;
  private running = false;

  constructor(
    private readonly timeoutMs: number,
    private readonly env: IdleEnv,
    private readonly on: IdleHandlers,
  ) {
    this.warnMs = warnWindowMs(timeoutMs);
  }

  get isRunning(): boolean {
    return this.running;
  }

  get isWarning(): boolean {
    return this.warned;
  }

  start(): void {
    this.stop();
    this.running = true;
    this.touch();
    this.schedule(this.timeoutMs - this.warnMs);
  }

  stop(): void {
    this.running = false;
    this.warned = false;
    this.cancel();
  }

  /**
   * The user did something. Ignored while the warning is up: only `stay()` answers it, so a
   * mouse twitch behind the dialog does not silently cancel it.
   */
  activity(): void {
    if (!this.running || this.warned) return;
    const n = this.env.now();
    this.last = n;
    if (n - this.lastWrite >= WRITE_EVERY_MS) this.touch();
  }

  /** The "Stay signed in" button. */
  stay(): void {
    if (!this.running) return;
    this.touch();
    if (this.warned) {
      this.warned = false;
      this.on.resume();
    }
    this.schedule(this.timeoutMs - this.warnMs);
  }

  /** Re-evaluates now. Called by the timer, and when another tab changed the shared time. */
  check(): void {
    if (!this.running) return;
    const n = this.env.now();
    const shared = this.env.read();
    if (shared !== null && shared > this.last) this.last = Math.min(shared, n);
    const left = this.timeoutMs - (n - this.last);
    if (left <= 0) {
      this.stop();
      this.on.expire();
    } else if (left <= this.warnMs) {
      if (this.warned) {
        this.on.tick(left);
      } else {
        this.warned = true;
        this.on.warn(left);
      }
      this.schedule(Math.min(1000, left));
    } else {
      if (this.warned) {
        this.warned = false;
        this.on.resume();
      }
      this.schedule(left - this.warnMs);
    }
  }

  private touch(): void {
    const n = this.env.now();
    this.last = n;
    this.lastWrite = n;
    this.env.write(n);
  }

  private schedule(ms: number): void {
    this.cancel();
    const wait = Math.max(MIN_WAIT_MS, Math.min(ms, MAX_WAIT_MS));
    this.handle = this.env.setTimeout(() => {
      this.handle = null;
      this.check();
    }, wait);
  }

  private cancel(): void {
    if (this.handle !== null) this.env.clearTimeout(this.handle);
    this.handle = null;
  }
}
