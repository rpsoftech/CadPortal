import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { RouterLink } from '@angular/router';
import { ApiService } from '../../core/api.service';
import { fixed } from '../../core/format';

/** Saved costing sheets, newest first. Each one is a frozen result; open it to see or download it. */
@Component({
  selector: 'app-costing-sheets',
  imports: [DatePipe, RouterLink, MatButtonModule, MatPaginatorModule, MatProgressBarModule],
  template: `
    <div class="page">
      <h1>Costing sheets</h1>
      <p class="muted">
        Saved costings. Each keeps the rates and prices it was made with, so a later change of gold
        rate or master data never alters it. To cost designs, select them in the
        <a routerLink="/">catalog</a> and choose “Cost selected”.
      </p>
      @if (sheets.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (sheets.error()) {
        <div class="error-box" role="alert">Could not load the costing sheets.</div>
      }
      @if (sheets.value(); as s) {
        @if (s.items.length === 0) {
          <p class="muted">No costing sheets yet.</p>
        } @else {
          <table class="plain" aria-label="Costing sheets">
            <thead>
              <tr>
                <th>Name</th>
                <th>Saved</th>
                <th>By</th>
                <th>Metal</th>
                <th class="num">Gold rate / g</th>
                <th class="num">Designs</th>
                <th class="num">Total</th>
              </tr>
            </thead>
            <tbody>
              @for (c of s.items; track c.id) {
                <tr>
                  <td>
                    <a [routerLink]="['/costing-sheets', c.id]">{{ c.name }}</a>
                    @if (c.incomplete_count) {
                      <span class="muted small"> · {{ c.incomplete_count }} not costed</span>
                    }
                  </td>
                  <td>{{ c.created_at | date: 'medium' }}</td>
                  <td>{{ c.created_by_name || '–' }}</td>
                  <td>{{ c.metal_code }} · {{ c.stone_code }}</td>
                  <td class="num">{{ fx(c.gold_rate_per_g, 2) }}</td>
                  <td class="num">{{ c.design_count }}</td>
                  <td class="num">{{ fx(c.total, 2) }}</td>
                </tr>
              }
            </tbody>
          </table>
          @if (s.total > pageSizeOptions[0]) {
            <mat-paginator
              [length]="s.total"
              [pageSize]="pageSize()"
              [pageIndex]="pageIndex()"
              [pageSizeOptions]="pageSizeOptions"
              (page)="onPage($event)"
              aria-label="Select page of costing sheets"
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
    .small {
      font-size: 0.85rem;
    }
  `,
})
export class CostingSheetsComponent {
  private readonly api = inject(ApiService);
  protected readonly pageSizeOptions = [25, 50, 100];
  protected readonly pageIndex = signal(0);
  protected readonly pageSize = signal(25);

  protected readonly sheets = rxResource({
    params: () => ({ i: this.pageIndex(), n: this.pageSize() }),
    stream: ({ params }) => this.api.costingSheets(params.n, params.i * params.n),
  });

  protected fx(v: string, dp: number): string {
    return fixed(v, dp);
  }

  protected onPage(e: PageEvent): void {
    this.pageIndex.set(e.pageIndex);
    this.pageSize.set(e.pageSize);
  }
}
