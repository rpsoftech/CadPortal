import { Component, inject } from '@angular/core';
import { MatTabsModule } from '@angular/material/tabs';
import { MatrixTabComponent } from './matrix-tab.component';
import { MetalsTabComponent } from './metals-tab.component';
import { RecalcWatcher } from './recalc-watcher';
import { TaxonomyTabComponent } from './taxonomy-tab.component';

@Component({
  selector: 'app-master-data',
  imports: [MatTabsModule, MetalsTabComponent, MatrixTabComponent, TaxonomyTabComponent],
  providers: [RecalcWatcher],
  template: `
    <div class="page">
      <h1>Master data</h1>

      <div class="banner" aria-live="polite">
        @if (recalc.busy()) {
          <div class="busy">
            Rebuilding weights for every variation ({{
              (recalc.status()?.pending ?? 0) + (recalc.status()?.running ?? 0)
            }}
            queued). Catalog numbers update when this finishes.
          </div>
        }
        @if (recalc.failedMessage(); as msg) {
          <div class="error-box" role="alert">
            A weight recalculation failed: {{ msg }}. Fix the data and save again to retry.
          </div>
        }
      </div>

      <mat-tab-group animationDuration="0ms">
        <mat-tab label="Alloys">
          <ng-template matTabContent><app-metals-tab /></ng-template>
        </mat-tab>
        <mat-tab label="Stone matrix">
          <ng-template matTabContent><app-matrix-tab /></ng-template>
        </mat-tab>
        <mat-tab label="Categories & tags">
          <ng-template matTabContent><app-taxonomy-tab /></ng-template>
        </mat-tab>
      </mat-tab-group>
    </div>
  `,
  styles: `
    h1 {
      margin: 0 0 8px;
    }
    .busy {
      padding: 8px 12px;
      border-radius: 8px;
      background: var(--mat-sys-secondary-container);
      color: var(--mat-sys-on-secondary-container);
      margin-bottom: 8px;
    }
    ::ng-deep .mat-mdc-tab-body-content {
      padding: 16px 0;
    }
  `,
})
export class MasterDataComponent {
  protected readonly recalc = inject(RecalcWatcher);
}
