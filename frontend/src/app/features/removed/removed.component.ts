import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Observable } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { TrashedFile } from '../../core/models';

/** Why a file left its place, in words. */
export function reasonText(reason: TrashedFile['reason']): string {
  return {
    removed: 'Removed',
    replaced: 'Replaced by a newer file',
    variation_deleted: 'Its variation was deleted',
  }[reason];
}

/** Whole days from now until `iso` (never below 0), for "kept for 29 more days". */
export function daysLeft(iso: string, now: Date = new Date()): number {
  return Math.max(0, Math.ceil((new Date(iso).getTime() - now.getTime()) / 86_400_000));
}

/**
 * The way back from a mistake: deleted designs, deleted variations, and CAD files, photos and videos that were removed or
 * replaced. Designs stay here until someone restores them; files are kept for a set number of days
 * and then deleted for good.
 */
@Component({
  selector: 'app-removed',
  imports: [DatePipe, MatButtonModule, MatProgressBarModule],
  template: `
    <div class="page">
      <h1>Removed</h1>
      <p class="muted">
        Deleted designs, deleted variations and removed files can be brought back. Removed and replaced files are kept
        for a limited time and then deleted for good.
      </p>
      @if (trash.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (trash.error()) {
        <div class="error-box" role="alert">Could not load the removed items.</div>
      }

      @if (trash.value(); as t) {
        <section aria-label="Deleted designs">
          <h2>Deleted designs</h2>
          @if (t.designs.length === 0) {
            <p class="muted">No deleted designs.</p>
          } @else {
            <table class="plain">
              <thead>
                <tr>
                  <th>Design</th>
                  <th>Category</th>
                  <th>Variations</th>
                  <th>Deleted</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                @for (d of t.designs; track d.id) {
                  <tr>
                    <td>
                      <span class="mono">{{ d.code }}</span> {{ d.name }}
                    </td>
                    <td>{{ d.category_name }}</td>
                    <td class="num">{{ d.variation_count }}</td>
                    <td>{{ d.deleted_at | date: 'medium' }}</td>
                    <td>
                      <button
                        matButton
                        [disabled]="busy()"
                        (click)="run(api.restoreDesign(d.id), 'Restored ' + d.code)"
                        [attr.aria-label]="'Restore design ' + d.code"
                      >
                        Restore
                      </button>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          }
        </section>

        <section aria-label="Deleted variations">
          <h2>Deleted variations</h2>
          @if (t.variations.length === 0) {
            <p class="muted">No deleted variations.</p>
          } @else {
            <table class="plain">
              <thead>
                <tr>
                  <th>Variation</th>
                  <th>Design</th>
                  <th>Deleted</th>
                  <th>Kept for</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                @for (v of t.variations; track v.id) {
                  <tr>
                    <td>
                      <span class="mono">{{ v.code }}</span>
                      @if (v.has_files) {
                        <span class="muted small">(with CAD files)</span>
                      }
                    </td>
                    <td>
                      <span class="mono">{{ v.design_code }}</span> {{ v.design_name }}
                    </td>
                    <td>{{ v.deleted_at | date: 'medium' }}</td>
                    <td>
                      @if (v.purge_after) {
                        {{ daysFor(v.purge_after) }} more day{{
                          daysFor(v.purge_after) === 1 ? '' : 's'
                        }}
                      } @else {
                        <span class="muted small">Not purged automatically</span>
                      }
                    </td>
                    <td>
                      @if (v.design_deleted) {
                        <span class="muted small">Restore the design first</span>
                      } @else {
                        <button
                          matButton
                          [disabled]="busy()"
                          (click)="run(api.restoreVariation(v.id), 'Restored ' + v.code)"
                          [attr.aria-label]="'Restore variation ' + v.code"
                        >
                          Restore
                        </button>
                      }
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          }
        </section>

        <section aria-label="Removed files">
          <h2>Removed files</h2>
          @if (t.files.length === 0) {
            <p class="muted">No removed files.</p>
          } @else {
            <table class="plain">
              <thead>
                <tr>
                  <th>File</th>
                  <th>What happened</th>
                  <th>When</th>
                  <th>Kept for</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                @for (f of t.files; track f.id) {
                  <tr>
                    <td>
                      {{ f.label }}
                      <span class="muted small">{{
                        f.kind === 'cad' ? '(CAD file)' : '(photo or video)'
                      }}</span>
                    </td>
                    <td>{{ reason(f) }}</td>
                    <td>{{ f.deleted_at | date: 'medium' }}</td>
                    <td>{{ days(f) }} more day{{ days(f) === 1 ? '' : 's' }}</td>
                    <td>
                      @if (f.restorable) {
                        <button
                          matButton
                          [disabled]="busy()"
                          (click)="run(api.restoreFile(f.id), 'Restored ' + f.label)"
                          [attr.aria-label]="'Restore ' + f.label"
                        >
                          Restore
                        </button>
                      } @else {
                        <span class="muted small">Cannot be put back</span>
                      }
                    </td>
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
  `,
})
export class RemovedComponent {
  protected readonly api = inject(ApiService);
  private readonly snack = inject(MatSnackBar);

  private readonly tick = signal(0);
  protected readonly trash = rxResource({
    params: () => this.tick(),
    stream: () => this.api.trash(),
  });
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected reason(f: TrashedFile): string {
    return reasonText(f.reason);
  }
  protected daysFor(iso: string): number {
    return daysLeft(iso);
  }
  protected days(f: TrashedFile): number {
    return daysLeft(f.purge_after);
  }

  /** Runs a restore and refreshes the lists; a refusal is shown in the server's words. */
  protected run(req: Observable<void>, done: string): void {
    this.busy.set(true);
    this.error.set('');
    req.subscribe({
      next: () => {
        this.busy.set(false);
        this.snack.open(done, 'OK', { duration: 4000 });
        this.tick.update((n) => n + 1);
      },
      error: async (e) => {
        this.busy.set(false);
        this.error.set((await toApiError(e)).message);
      },
    });
  }
}
