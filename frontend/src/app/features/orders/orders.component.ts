import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { rxResource, toSignal } from '@angular/core/rxjs-interop';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { RouterLink } from '@angular/router';
import { debounceTime, distinctUntilChanged, map, startWith } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { ORDER_STATUSES, ORDER_STATUS_LABEL } from '../../core/models';
import { OrderStatusComponent } from './order-status.component';

/** Orders, newest first. Everyone signed in reads; admin and designer start new ones from the catalog. */
@Component({
  selector: 'app-orders',
  imports: [
    DatePipe,
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    OrderStatusComponent,
  ],
  template: `
    <div class="page">
      <h1>Orders</h1>
      <p class="muted">
        A selection of designs sent to production, kept exactly as it was when the order was made.
        @if (auth.canEdit()) {
          To start one, select designs in the <a routerLink="/">catalog</a> and choose “Create order”.
        }
      </p>

      <form class="row filters" aria-label="Filter orders" (submit)="$event.preventDefault()">
        <mat-form-field appearance="outline" class="q">
          <mat-label>Search number, title or notes</mat-label>
          <input matInput type="search" [formControl]="q" autocomplete="off" />
        </mat-form-field>
        <mat-form-field appearance="outline" class="status">
          <mat-label>Status</mat-label>
          <mat-select [formControl]="status">
            <mat-option value="">All</mat-option>
            @for (s of statuses; track s.value) {
              <mat-option [value]="s.value">{{ s.label }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
      </form>

      @if (orders.isLoading()) {
        <mat-progress-bar mode="indeterminate" aria-label="Loading orders" />
      }
      @if (orders.error()) {
        <div class="error-box" role="alert">Could not load the orders.</div>
      }
      @if (orders.value(); as o) {
        <p class="muted" role="status">
          {{ o.total }} order{{ o.total === 1 ? '' : 's' }}
        </p>
        @if (o.items.length === 0) {
          <p class="muted empty">
            @if (filtered()) {
              No order matches this filter.
            } @else {
              No orders yet.
            }
          </p>
        } @else {
          <table class="plain stack" aria-label="Orders">
            <thead>
              <tr>
                <th scope="col">Number</th>
                <th scope="col">Title</th>
                <th scope="col">Status</th>
                <th scope="col">Metal · stone</th>
                <th scope="col" class="num">Lines</th>
                <th scope="col" class="num">Pieces</th>
                <th scope="col">Due</th>
                <th scope="col">Created</th>
              </tr>
            </thead>
            <tbody>
              @for (r of o.items; track r.id) {
                <tr>
                  <td data-label="Number">
                    <a class="mono" [routerLink]="['/orders', r.id]">{{ r.number }}</a>
                  </td>
                  <td data-label="Title" class="title">{{ r.name || '–' }}</td>
                  <td data-label="Status"><app-order-status [status]="r.status" /></td>
                  <td data-label="Metal · stone">{{ r.metal_code }} · {{ r.stone_code }}</td>
                  <td data-label="Lines" class="num">{{ r.line_count }}</td>
                  <td data-label="Pieces" class="num">{{ r.total_quantity }}</td>
                  <td data-label="Due">{{ r.due_date ? (r.due_date | date: 'mediumDate') : '–' }}</td>
                  <td data-label="Created">
                    {{ r.created_at | date: 'medium' }}
                    @if (r.created_by_name) {
                      <span class="muted"> · {{ r.created_by_name }}</span>
                    }
                  </td>
                </tr>
              }
            </tbody>
          </table>
          @if (o.total > pageSizeOptions[0]) {
            <mat-paginator
              [length]="o.total"
              [pageSize]="pageSize()"
              [pageIndex]="pageIndex()"
              [pageSizeOptions]="pageSizeOptions"
              (page)="onPage($event)"
              showFirstLastButtons
              aria-label="Select page of orders"
            />
          }
        }
      }
    </div>
  `,
  styles: `
    h1 {
      margin: 0 0 8px;
    }
    .filters {
      gap: 12px;
      align-items: flex-start;
    }
    .q {
      flex: 1;
      min-width: 220px;
    }
    .status {
      width: 200px;
    }
    .title {
      max-width: 28ch;
      overflow-wrap: anywhere;
    }
    @media (max-width: 720px) {
      .status {
        width: 100%;
      }
      .title {
        max-width: none;
      }
    }
  `,
})
export class OrdersComponent {
  private readonly api = inject(ApiService);
  protected readonly auth = inject(AuthService);

  protected readonly statuses = ORDER_STATUSES.map((value) => ({ value, label: ORDER_STATUS_LABEL[value] }));
  protected readonly q = new FormControl('', { nonNullable: true });
  protected readonly status = new FormControl('', { nonNullable: true });
  protected readonly pageSizeOptions = [25, 50, 100];
  protected readonly pageIndex = signal(0);
  protected readonly pageSize = signal(25);

  private readonly qValue = toSignal(
    this.q.valueChanges.pipe(startWith(''), debounceTime(300), map((v) => v.trim()), distinctUntilChanged()),
    { initialValue: '' },
  );
  private readonly statusValue = toSignal(this.status.valueChanges.pipe(startWith('')), { initialValue: '' });

  protected readonly orders = rxResource({
    params: () => ({
      q: this.qValue(),
      status: this.statusValue(),
      i: this.pageIndex(),
      n: this.pageSize(),
    }),
    stream: ({ params }) =>
      this.api.orders({ q: params.q, status: params.status, limit: params.n, offset: params.i * params.n }),
  });

  protected filtered(): boolean {
    return this.qValue() !== '' || this.statusValue() !== '';
  }

  constructor() {
    // a new filter starts again from the first page
    this.q.valueChanges.subscribe(() => this.pageIndex.set(0));
    this.status.valueChanges.subscribe(() => this.pageIndex.set(0));
  }

  protected onPage(e: PageEvent): void {
    this.pageIndex.set(e.pageIndex);
    this.pageSize.set(e.pageSize);
  }
}
