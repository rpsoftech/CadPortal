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
 * The way back from a mistake: deleted designs, and CAD files, photos and videos that were removed or
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
        Deleted designs and removed files can be brought back. Removed and replaced files are kept
        for a limited time and then deleted for good.
      </p>
      @if (trash.isLoading()) {
        <mat-progress-bar aria-label="Loading" mode="indeterminate" />
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
            <table class="plain stack" aria-label="Deleted designs">
              <thead>
                <tr>
                  <th scope="col">Design</th>
                  <th scope="col">Category</th>
                  <th scope="col">Variations</th>
                  <th scope="col">Deleted</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                @for (d of t.designs; track d.id) {
                  <tr>
                    <td data-label="Design">
                      <span class="mono">{{ d.code }}</span> {{ d.name }}
                    </td>
                    <td data-label="Category">{{ d.category_name }}</td>
                    <td data-label="Variations" class="num">{{ d.variation_count }}</td>
                    <td data-label="Deleted">{{ d.deleted_at | date: 'medium' }}</td>
                    <td class="row-actions">
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

        <section aria-label="Removed files">
          <h2>Removed files</h2>
          @if (t.files.length === 0) {
            <p class="muted">No removed files.</p>
          } @else {
            <table class="plain stack" aria-label="Removed files">
              <thead>
                <tr>
                  <th scope="col">File</th>
                  <th scope="col">What happened</th>
                  <th scope="col">When</th>
                  <th scope="col">Kept for</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                @for (f of t.files; track f.id) {
                  <tr>
                    <td data-label="File">
                      {{ f.label }}
                      <span class="muted small">{{
                        f.kind === 'cad' ? '(CAD file)' : '(photo or video)'
                      }}</span>
                    </td>
                    <td data-label="What happened">{{ reason(f) }}</td>
                    <td data-label="When">{{ f.deleted_at | date: 'medium' }}</td>
                    <td data-label="Kept for">{{ days(f) }} more day{{ days(f) === 1 ? '' : 's' }}</td>
                    <td class="row-actions">
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
