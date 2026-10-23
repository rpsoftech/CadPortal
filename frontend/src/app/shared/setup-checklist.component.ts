import { Component, inject, input } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { catchError, of } from 'rxjs';
import { ApiService } from '../core/api.service';
import { SetupItem } from '../core/models';

/** Master data tabs, in the order they appear on the Master data screen. */
export const SETUP_TABS: SetupItem['tab'][] = ['alloys', 'matrix', 'categories'];

/** The Master data tab index for a query-string value; unknown values mean the first tab. */
export function tabIndex(tab: string | null | undefined): number {
  const i = SETUP_TABS.indexOf(tab as SetupItem['tab']);
  return i < 0 ? 0 : i;
}

/** Counts of items by severity, for the one-line summary. */
export function summarize(items: SetupItem[]): { required: number; recommended: number } {
  return {
    required: items.filter((i) => i.severity === 'required').length,
    recommended: items.filter((i) => i.severity === 'recommended').length,
  };
}

/**
 * What an admin still has to enter (wastage, default alloy, category prefixes, stone matrix).
 * It reloads whenever master data is saved anywhere, so items disappear as they are fixed.
 * Admin only: the endpoint talks about wastage and rates.
 */
@Component({
  selector: 'app-setup-checklist',
  imports: [RouterLink],
  template: `
    @if (status.value(); as st) {
      @if (!st.complete) {
        @if (compact()) {
          <div class="banner" role="status">
            <strong>Setup is not finished.</strong>
            {{ sum(st.items) }}
            <a [routerLink]="['/master-data']" [queryParams]="{ tab: st.items[0].tab || null }">
              Open the checklist
            </a>
          </div>
        } @else {
          <details class="checklist" open>
            <summary>
              <strong>Finish setting up</strong>
              <span class="muted">{{ sum(st.items) }}</span>
            </summary>
            <ul>
              @for (it of st.items; track it.key) {
                <li [class]="it.severity">
                  <div class="what">
                    <strong>{{ it.title }}</strong>
                    <span class="tag">{{ it.severity }}</span>
                    <span class="detail">{{ it.detail }}</span>
                  </div>
                  @if (it.tab) {
                    <a [routerLink]="['/master-data']" [queryParams]="{ tab: it.tab }">
                      {{ tabLabel(it.tab) }} →
                    </a>
                  }
                </li>
              }
            </ul>
          </details>
        }
      }
    }
  `,
  styles: `
    .banner,
    .checklist {
      padding: 12px 16px;
      border-radius: 12px;
      background: var(--mat-sys-tertiary-container);
      color: var(--mat-sys-on-tertiary-container);
      margin-bottom: 16px;
    }
    .banner a,
    .checklist a {
      color: inherit;
      font-weight: 500;
    }
    summary {
      cursor: pointer;
      display: flex;
      flex-wrap: wrap;
      gap: 4px 12px;
    }
    .muted {
      opacity: 0.85;
    }
    ul {
      list-style: none;
      margin: 8px 0 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    li {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 16px;
      padding-left: 10px;
      border-left: 4px solid var(--mat-sys-outline);
    }
    .detail {
      margin-left: 8px;
      opacity: 0.9;
    }
    li a {
      white-space: nowrap;
    }
    @media (max-width: 720px) {
      li {
        flex-wrap: wrap;
        gap: 4px 16px;
      }
      .what {
        flex: 1 1 100%;
        min-width: 0;
      }
    }
    li.required {
      border-left-color: var(--mat-sys-error);
    }
    .tag {
      margin-left: 8px;
      padding: 1px 8px;
      border-radius: 10px;
      font-size: 0.75rem;
      background: var(--mat-sys-surface-container-high);
      color: var(--mat-sys-on-surface);
    }
  `,
})
export class SetupChecklistComponent {
  private readonly api = inject(ApiService);

  /** One line with a link, for the catalog; the full list is on the Master data screen. */
  readonly compact = input(false);

  protected readonly status = rxResource({
    params: () => this.api.mastersChanged(),
    stream: () => this.api.setupStatus().pipe(catchError(() => of(null))),
  });

  protected sum(items: SetupItem[]): string {
    const s = summarize(items);
    const parts = [
      s.required ? `${s.required} required` : '',
      s.recommended ? `${s.recommended} recommended` : '',
    ].filter(Boolean);
    return `${parts.join(' and ')} item${items.length === 1 ? '' : 's'} left in Master data.`;
  }

  protected tabLabel(tab: SetupItem['tab']): string {
    return { alloys: 'Alloys', matrix: 'Stone matrix', categories: 'Categories & tags', '': '' }[tab];
  }
}
