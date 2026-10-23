import { DestroyRef, Injectable, effect, inject, signal, untracked } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { catchError, of } from 'rxjs';
import {
  IdleWarningData,
  IdleWarningDialogComponent,
} from '../shared/idle-warning-dialog.component';
import { ApiService } from './api.service';
import { AuthService } from './auth.service';
import { IdleEnv, IdleTimer } from './idle-timer';

/** Shared by every tab of this browser: when anyone last used the portal. */
export const ACTIVITY_KEY = 'cadportal.lastActivity';

/** What counts as using the portal. Requests the app makes on its own do not. */
const ACTIVITY_EVENTS = ['pointerdown', 'pointermove', 'keydown', 'touchstart', 'wheel', 'scroll'];

function browserEnv(): IdleEnv {
  return {
    now: () => Date.now(),
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    read: () => {
      try {
        const v = Number(localStorage.getItem(ACTIVITY_KEY));
        return Number.isFinite(v) && v > 0 ? v : null;
      } catch {
        return null;
      }
    },
    write: (ms) => {
      try {
        localStorage.setItem(ACTIVITY_KEY, String(ms));
      } catch {
        /* storage unavailable: this tab still times out on its own activity */
      }
    },
  };
}

/**
 * Signs the user out after a period without activity. The period comes from the server
 * (`idle_timeout_minutes` in /auth/config, 0 = off), so operators change it without a rebuild.
 * About a minute before, a dialog offers "Stay signed in". Activity in any tab of the browser
 * keeps all of them signed in (shared through localStorage).
 */
@Injectable({ providedIn: 'root' })
export class IdleService {
  private readonly auth = inject(AuthService);
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);

  /** Seconds left while the warning is up, else null. */
  readonly secondsLeft = signal<number | null>(null);

  private timer: IdleTimer | null = null;
  private dialogRef: MatDialogRef<IdleWarningDialogComponent> | null = null;
  private detach: (() => void) | null = null;
  private generation = 0;

  constructor() {
    effect(() => {
      const signedIn = this.auth.isAuthenticated();
      untracked(() => (signedIn ? this.begin() : this.end()));
    });
    inject(DestroyRef).onDestroy(() => this.end());
  }

  /** The "Stay signed in" button. */
  stay(): void {
    this.timer?.stay();
  }

  private begin(): void {
    if (this.timer) return;
    const gen = ++this.generation;
    this.api
      .authConfig()
      .pipe(catchError(() => of(null)))
      .subscribe((cfg) => {
        const minutes = Number(cfg?.idle_timeout_minutes ?? 0);
        if (gen !== this.generation || !this.auth.isAuthenticated() || !(minutes > 0)) return;
        this.run(minutes * 60_000);
      });
  }

  private run(timeoutMs: number): void {
    const timer = new IdleTimer(timeoutMs, browserEnv(), {
      warn: (left) => {
        this.secondsLeft.set(Math.ceil(left / 1000));
        this.dialogRef ??= this.dialog.open<IdleWarningDialogComponent, IdleWarningData>(
          IdleWarningDialogComponent,
          {
            disableClose: true,
            width: '420px',
            data: { secondsLeft: this.secondsLeft, stay: () => this.stay() },
          },
        );
      },
      tick: (left) => this.secondsLeft.set(Math.ceil(left / 1000)),
      resume: () => this.closeDialog(),
      expire: () => {
        this.closeDialog();
        this.auth.expireIdle();
      },
    });
    this.timer = timer;

    const onActivity = () => timer.activity();
    const onStorage = (e: StorageEvent) => {
      if (e.key === ACTIVITY_KEY || e.key === null) timer.check();
    };
    // Wake-ups after sleep: timers are late, so look at the clock again when the tab is shown.
    const onVisible = () => {
      if (document.visibilityState === 'visible') timer.check();
    };
    for (const ev of ACTIVITY_EVENTS) {
      document.addEventListener(ev, onActivity, { capture: true, passive: true });
    }
    window.addEventListener('storage', onStorage);
    document.addEventListener('visibilitychange', onVisible);
    this.detach = () => {
      for (const ev of ACTIVITY_EVENTS) document.removeEventListener(ev, onActivity, true);
      window.removeEventListener('storage', onStorage);
      document.removeEventListener('visibilitychange', onVisible);
    };
    timer.start();
  }

  private end(): void {
    this.generation++;
    this.timer?.stop();
    this.timer = null;
    this.detach?.();
    this.detach = null;
    this.closeDialog();
  }

  private closeDialog(): void {
    this.secondsLeft.set(null);
    this.dialogRef?.close();
    this.dialogRef = null;
  }
}
