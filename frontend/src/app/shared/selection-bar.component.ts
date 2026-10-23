import { Component, inject, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { CatalogSelection } from '../core/catalog-selection';

/**
 * A bar stuck to the bottom of the screen while designs are selected: how many, a Clear button, and
 * whatever the page projects into it (each bulk action adds its own button).
 *
 *   <app-selection-bar><button matButton="filled" (click)="…">Cost selected</button></app-selection-bar>
 */
@Component({
  selector: 'app-selection-bar',
  imports: [MatButtonModule],
  template: `
    @if (!selection.isEmpty()) {
      <div class="bar" role="region" [attr.aria-label]="label()">
        <span class="count" aria-live="polite">{{ selection.count() }} selected</span>
        <button matButton type="button" (click)="selection.clear()">Clear</button>
        <span class="spacer"></span>
        <ng-content />
      </div>
    }
  `,
  styles: `
    .bar {
      position: sticky;
      bottom: 0;
      z-index: 5;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px 12px;
      margin: 16px -16px -16px;
      padding: 10px 16px;
      background: var(--mat-sys-surface-container-high);
      border-top: 1px solid var(--mat-sys-outline-variant);
      box-shadow: 0 -2px 8px rgb(0 0 0 / 12%);
    }
    .count {
      font-weight: 600;
    }
    .spacer {
      flex: 1;
    }
  `,
})
export class SelectionBarComponent {
  protected readonly selection = inject(CatalogSelection);
  readonly label = input('Selected designs');
}
