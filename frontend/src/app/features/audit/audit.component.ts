import { DatePipe } from '@angular/common';
import { Component, computed, inject, linkedSignal, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { catchError, of } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { AuditFilter } from '../../core/models';

/** Action groups for the filter: the part of the action before the dot. */
export const ACTION_GROUPS: { prefix: string; label: string }[] = [
  { prefix: '', label: 'All actions' },
  { prefix: 'auth.', label: 'Sign-in and passwords' },
  { prefix: 'user.', label: 'Users' },
  { prefix: 'metal.', label: 'Alloys' },
  { prefix: 'stone_matrix.', label: 'Stone matrix' },
  { prefix: 'stone_code.', label: 'Stone codes' },
  { prefix: 'category.', label: 'Categories' },
  { prefix: 'tag.', label: 'Tags' },
  { prefix: 'design.', label: 'Designs' },
  { prefix: 'variation.', label: 'Variations and seats' },
  { prefix: 'cad.', label: 'CAD files' },
  { prefix: 'export.', label: 'Exports' },
];

export type StatusKind = 'ok' | 'denied' | 'rejected' | 'error';

/** How to present an HTTP status: success, refused for lack of permission, rejected, or failed. */
export function statusKind(status: number): StatusKind {
  if (status === 403) return 'denied';
  if (status >= 500) return 'error';
  if (status >= 400) return 'rejected';
  return 'ok';
}

@Component({
  selector: 'app-audit',
  imports: [
    DatePipe,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
  ],
  template: `
    <div class="page">
      <h1>Audit log</h1>
      <p class="muted">
        Who changed what, newest first. Refused attempts are listed too. Request contents, passwords
        and tokens are never recorded.
      </p>

      <div class="row filters">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Actions</mat-label>
          <mat-select
            [value]="filters().action"
            (selectionChange)="patch({ action: $event.value })"
          >
            @for (g of groups; track g.prefix) {
              <mat-option [value]="g.prefix">{{ g.label }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>User</mat-label>
          <mat-select
            [value]="filters().userId"
            (selectionChange)="patch({ userId: $event.value })"
          >
            <mat-option [value]="null">Everyone</mat-option>
            @for (u of users.value() ?? []; track u.id) {
              <mat-option [value]="u.id">{{ u.email }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>From</mat-label>
          <input
            matInput
            type="date"
            [value]="filters().from"
            (change)="patch({ from: $any($event.target).value })"
          />
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>To</mat-label>
          <input
            matInput
            type="date"
            [value]="filters().to"
            (change)="patch({ to: $any($event.target).value })"
          />
        </mat-form-field>
        <button matButton (click)="clear()">Clear</button>
      </div>

      @if (log.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (log.error()) {
        <div class="error-box" role="alert">Could not load the audit log.</div>
      }

      <div class="scroll">
        <table class="plain">
          <thead>
            <tr>
              <th>When</th>
              <th>User</th>
              <th>Action</th>
              <th>Entity</th>
              <th>Detail</th>
              <th>Result</th>
            </tr>
          </thead>
          <tbody>
            @for (e of rows(); track e.id) {
              <tr [class]="'kind-' + kind(e.status)">
                <td class="nowrap">{{ e.at | date: 'medium' }}</td>
                <td>{{ e.user_email || '(not signed in)' }}</td>
                <td class="mono">{{ e.action }}</td>
                <td>
                  @if (e.entity_type) {
                    {{ e.entity_type }}{{ e.entity_id ? ' #' + e.entity_id : '' }}
                  }
                </td>
                <td class="detail">{{ e.detail }}</td>
                <td>
                  <span class="badge">{{ label(e.status) }}</span>
                  <span class="muted small">{{ e.status }}</span>
                </td>
              </tr>
            } @empty {
              @if (!log.isLoading()) {
                <tr>
                  <td colspan="6" class="muted">No entries match.</td>
                </tr>
              }
            }
          </tbody>
        </table>
      </div>

      @if (total() > 0) {
        <mat-paginator
          [length]="total()"
          [pageSize]="pageSize()"
          [pageIndex]="pageIndex()"
          [pageSizeOptions]="[25, 50, 100, 200]"
          (page)="onPage($event)"
          showFirstLastButtons
          aria-label="Select page of audit entries"
        />
      }
    </div>
  `,
  styles: `
    h1 {
      margin: 0 0 4px;
    }
    .filters {
      margin: 12px 0;
    }
    .scroll {
      overflow-x: auto;
    }
    .nowrap {
      white-space: nowrap;
    }
    .detail {
      max-width: 360px;
      overflow-wrap: anywhere;
    }
    .small {
      font-size: 0.8rem;
      margin-left: 4px;
    }
    .badge {
      padding: 2px 8px;
      border-radius: 10px;
      background: var(--mat-sys-surface-container-high);
    }
    tr.kind-denied .badge {
      background: var(--mat-sys-error-container);
      color: var(--mat-sys-on-error-container);
    }
    tr.kind-error .badge {
      background: var(--mat-sys-error);
      color: var(--mat-sys-on-error);
    }
  `,
})
export class AuditComponent {
  private readonly api = inject(ApiService);

  protected readonly groups = ACTION_GROUPS;
  protected readonly users = rxResource({
    params: () => 1,
    stream: () => this.api.users().pipe(catchError(() => of([]))),
  });

  protected readonly filters = signal({
    action: '',
    userId: null as number | null,
    from: '',
    to: '',
  });
  protected readonly pageSize = signal(50);
  /** Any filter change starts again from the first page. */
  protected readonly pageIndex = linkedSignal<unknown, number>({
    source: () => this.filters(),
    computation: () => 0,
  });

  protected readonly log = rxResource({
    params: () => ({
      f: this.filters(),
      size: this.pageSize(),
      index: this.pageIndex(),
    }),
    stream: ({ params }) => {
      const f: AuditFilter = {
        action: params.f.action,
        userId: params.f.userId,
        from: params.f.from,
        to: params.f.to,
        limit: params.size,
        offset: params.index * params.size,
      };
      return this.api.audit(f);
    },
  });

  protected readonly rows = computed(() => this.log.value()?.items ?? []);
  protected readonly total = computed(() => this.log.value()?.total ?? 0);

  protected patch(change: Partial<ReturnType<typeof this.filters>>): void {
    this.filters.update((f) => ({ ...f, ...change }));
  }

  protected clear(): void {
    this.filters.set({ action: '', userId: null, from: '', to: '' });
  }

  protected onPage(e: PageEvent): void {
    this.pageSize.set(e.pageSize);
    this.pageIndex.set(e.pageIndex);
  }

  protected kind(status: number): StatusKind {
    return statusKind(status);
  }

  protected label(status: number): string {
    const k = statusKind(status);
    return k === 'ok' ? 'OK' : k === 'denied' ? 'Denied' : k === 'error' ? 'Failed' : 'Rejected';
  }
}
