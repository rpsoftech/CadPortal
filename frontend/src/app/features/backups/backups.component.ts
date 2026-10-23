import { DatePipe } from '@angular/common';
import { Component, DestroyRef, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { formatBytes } from '../../core/media';
import { BackupRun } from '../../core/models';

export type RunState = 'running' | 'ok' | 'failed' | 'interrupted';

/** A run that never recorded an end is still running only while it is recent; otherwise it was killed. */
const RUNNING_WINDOW_MS = 3 * 3_600_000;

export function runState(run: BackupRun, now: Date = new Date()): RunState {
  if (run.finished_at === null) {
    return now.getTime() - new Date(run.started_at).getTime() < RUNNING_WINDOW_MS
      ? 'running'
      : 'interrupted';
  }
  return run.ok ? 'ok' : 'failed';
}

export function runLabel(state: RunState): string {
  return {
    running: 'Running',
    ok: 'Succeeded',
    failed: 'Failed',
    interrupted: 'Interrupted',
  }[state];
}

/** What a run copied, in words: "3 new of 120 files (12.4 MB)", or why nothing was. */
export function filesText(run: BackupRun): string {
  if (run.files_skipped) return 'No file storage';
  if (run.finished_at === null) return '';
  return `${run.files_copied} new of ${run.files_total} (${formatBytes(run.files_bytes)})`;
}

/** The newest finished run that worked, for the headline. */
export function lastGood(runs: BackupRun[]): BackupRun | undefined {
  return runs.find((r) => r.finished_at !== null && r.ok === true);
}

/** How long ago, coarsely: "just now", "5 hours ago", "3 days ago". */
export function ago(iso: string, now: Date = new Date()): string {
  const mins = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 60_000));
  if (mins < 2) return 'just now';
  if (mins < 120) return `${mins} minutes ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} hours ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

/**
 * Backups at a glance (admin): when the last good one finished, every recent run, the stored database
 * dumps with a download link, and "Back up now". Dumps are encrypted, so a download is useless
 * without the secret key that is kept offline.
 */
@Component({
  selector: 'app-backups',
  imports: [DatePipe, MatButtonModule, MatProgressBarModule],
  template: `
    <div class="page">
      <h1>Backups</h1>
      <p class="muted">
        The database is dumped every night and new files are copied to a second bucket. Everything
        here is for admins only.
      </p>
      @if (data.isLoading() && !data.value()) {
        <mat-progress-bar aria-label="Loading" mode="indeterminate" />
      }
      @if (data.error()) {
        <div class="error-box" role="alert">Could not load the backups.</div>
      }

      @if (data.value(); as b) {
        <section class="status" aria-label="Status">
          @if (good(); as g) {
            <p>
              <strong>Last good backup:</strong> {{ ago(g.finished_at!) }}
              <span class="muted">({{ g.finished_at | date: 'medium' }})</span>
            </p>
          } @else {
            <p><strong>No backup has completed yet.</strong></p>
          }
          <div class="actions">
            <button
              matButton="filled"
              [disabled]="b.pending || b.running || busy()"
              (click)="backUpNow()"
            >
              Back up now
            </button>
            @if (b.running) {
              <span class="muted" role="status">A backup is running…</span>
            } @else if (b.pending) {
              <span class="muted" role="status">
                Requested. The backup service starts it within about a minute.
              </span>
            }
          </div>
        </section>

        <section aria-label="Stored database dumps">
          <h2>Database dumps</h2>
          @if (b.dumps_note) {
            <p class="muted">{{ b.dumps_note }}</p>
          } @else if (b.dumps.length === 0) {
            <p class="muted">No dumps in the backup bucket yet.</p>
          } @else {
            <table class="plain stack" aria-label="Stored database dumps">
              <thead>
                <tr>
                  <th scope="col">Taken</th>
                  <th scope="col">File</th>
                  <th scope="col" class="num">Size</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                @for (d of b.dumps; track d.key) {
                  <tr>
                    <td data-label="Taken">{{ d.taken_at | date: 'medium' }}</td>
                    <td data-label="File" class="mono">{{ d.name }}</td>
                    <td data-label="Size" class="num">{{ bytes(d.bytes) }}</td>
                    <td class="row-actions">
                      <button
                        matButton
                        [disabled]="busy()"
                        (click)="download(d.key)"
                        [attr.aria-label]="'Download ' + d.name"
                      >
                        Download
                      </button>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
            <p class="muted small">
              A dump is encrypted (.age) and holds every password hash, the audit log, wastage and
              stone rates. It opens only with the secret key you stored offline when you set backups
              up; keep downloaded copies as private as that key. Each download is recorded in the
              audit log.
            </p>
          }
        </section>

        <section aria-label="Recent runs">
          <h2>Recent runs</h2>
          @if (b.runs.length === 0) {
            <p class="muted">The backup service has not run yet.</p>
          } @else {
            <table class="plain stack" aria-label="Recent backup runs">
              <thead>
                <tr>
                  <th scope="col">Started</th>
                  <th scope="col">Result</th>
                  <th scope="col" class="num">Database</th>
                  <th scope="col">Files</th>
                  <th scope="col">Problem</th>
                </tr>
              </thead>
              <tbody>
                @for (r of b.runs; track r.id) {
                  <tr>
                    <td data-label="Started">{{ r.started_at | date: 'medium' }}</td>
                    <td data-label="Result">
                      <span class="state" [class]="state(r)">{{ label(r) }}</span>
                    </td>
                    <td data-label="Database" class="num">{{ r.db_bytes ? bytes(r.db_bytes) : '' }}</td>
                    <td data-label="Files">{{ files(r) }}</td>
                    <td data-label="Problem" class="problem">{{ r.error }}</td>
                  </tr>
                }
              </tbody>
            </table>
          }
        </section>
      }
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      }
    </div>
  `,
  styles: `
    h1,
    h2 {
      margin: 0 0 8px;
    }
    section {
      margin-top: 24px;
    }
    .actions {
      display: flex;
      align-items: center;
      gap: 12px;
    }
    .state {
      font-weight: 500;
    }
    .state.ok {
      color: var(--mat-sys-primary);
    }
    .state.failed,
    .state.interrupted {
      color: var(--mat-sys-error);
    }
    .problem {
      max-width: 40ch;
      overflow-wrap: anywhere;
    }
  `,
})
export class BackupsComponent {
  private readonly api = inject(ApiService);
  private readonly snack = inject(MatSnackBar);

  private readonly tick = signal(0);
  protected readonly data = rxResource({
    params: () => this.tick(),
    stream: () => this.api.backups(),
  });
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected readonly ago = ago;
  protected readonly bytes = formatBytes;

  constructor() {
    // While a backup is waiting or running, look again every few seconds so the screen catches up.
    const timer = setInterval(() => {
      const b = this.data.value();
      if (b && (b.pending || b.running)) this.tick.update((n) => n + 1);
    }, 5000);
    inject(DestroyRef).onDestroy(() => clearInterval(timer));
  }

  protected good(): BackupRun | undefined {
    return lastGood(this.data.value()?.runs ?? []);
  }
  protected state(r: BackupRun): RunState {
    return runState(r);
  }
  protected label(r: BackupRun): string {
    return runLabel(runState(r));
  }
  protected files(r: BackupRun): string {
    return filesText(r);
  }

  protected backUpNow(): void {
    this.busy.set(true);
    this.error.set('');
    this.api.requestBackup().subscribe({
      next: () => {
        this.busy.set(false);
        this.snack.open('Backup requested', 'OK', { duration: 4000 });
        this.tick.update((n) => n + 1);
      },
      error: async (e) => {
        this.busy.set(false);
        this.error.set((await toApiError(e)).message);
        this.tick.update((n) => n + 1);
      },
    });
  }

  protected download(key: string): void {
    this.busy.set(true);
    this.error.set('');
    this.api.backupDownloadLink(key).subscribe({
      next: (l) => {
        this.busy.set(false);
        window.location.assign(l.url);
      },
      error: async (e) => {
        this.busy.set(false);
        this.error.set((await toApiError(e)).message);
      },
    });
  }
}
