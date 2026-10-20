import { DestroyRef, Injectable, computed, inject, signal } from '@angular/core';
import { ApiService } from '../../core/api.service';
import { RecalcStatus } from '../../core/models';

const POLL_MS = 1500;
const MAX_POLLS = 80; // about two minutes

/**
 * Master-data changes (SG, baseline stone, matrix carats) queue a background job that
 * rebuilds every variation's weights. This polls the queue so the screen can say when
 * the catalog numbers are current again. Provided per master-data page.
 */
@Injectable()
export class RecalcWatcher {
  private readonly api = inject(ApiService);
  private timer: ReturnType<typeof setInterval> | null = null;

  readonly status = signal<RecalcStatus | null>(null);
  readonly busy = computed(() => {
    const s = this.status();
    return !!s && s.pending + s.running > 0;
  });
  readonly failedMessage = computed(() => {
    const s = this.status();
    return s && s.failed > 0 ? s.last_error || 'A weight recalculation failed.' : '';
  });
  /** Bumped each time the queue drains, so views can reload derived data. */
  readonly settled = signal(0);

  constructor() {
    inject(DestroyRef).onDestroy(() => this.stop());
    this.refresh();
  }

  refresh(): void {
    this.api.recalcStatus().subscribe({ next: (s) => this.status.set(s), error: () => undefined });
  }

  /** Call after a save that queues a recalc; polls until the queue is idle. */
  watch(): void {
    this.stop();
    let polls = 0;
    this.timer = setInterval(() => {
      polls++;
      this.api.recalcStatus().subscribe({
        next: (s) => {
          const wasBusy = this.busy();
          this.status.set(s);
          if (s.pending + s.running === 0 || polls >= MAX_POLLS) {
            this.stop();
            if (wasBusy || s.pending + s.running === 0) this.settled.update((n) => n + 1);
          }
        },
        error: () => this.stop(),
      });
    }, POLL_MS);
    // Show "queued" right away instead of waiting for the first tick.
    this.refresh();
  }

  private stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }
}
