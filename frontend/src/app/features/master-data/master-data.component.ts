import { Component, inject, linkedSignal, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTabsModule } from '@angular/material/tabs';
import { ActivatedRoute } from '@angular/router';
import { map } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { saveBlob } from '../../shared/download';
import { SetupChecklistComponent, tabIndex } from '../../shared/setup-checklist.component';
import { MatrixTabComponent } from './matrix-tab.component';
import { MetalsTabComponent } from './metals-tab.component';
import { RecalcWatcher } from './recalc-watcher';
import { TaxonomyTabComponent } from './taxonomy-tab.component';

@Component({
  selector: 'app-master-data',
  imports: [
    MatTabsModule,
    MatButtonModule,
    MetalsTabComponent,
    MatrixTabComponent,
    TaxonomyTabComponent,
    SetupChecklistComponent,
  ],
  providers: [RecalcWatcher],
  template: `
    <div class="page">
      <div class="head">
        <h1>Master data</h1>
        <span class="spacer"></span>
        <button
          matButton
          (click)="exportAll()"
          [disabled]="exporting()"
          title="Alloys with wastage, stone codes, the stone matrix with rates, categories and tags, as CSV files in one zip"
        >
          Export all (ZIP)
        </button>
      </div>

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

      <app-setup-checklist />

      <mat-tab-group
        animationDuration="0ms"
        [selectedIndex]="selectedTab()"
        (selectedIndexChange)="selectedTab.set($event)"
      >
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
    .head {
      display: flex;
      align-items: center;
      gap: 12px;
    }
    .spacer {
      flex: 1;
    }
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
  private readonly api = inject(ApiService);
  private readonly snack = inject(MatSnackBar);
  protected readonly exporting = signal(false);

  /** Downloads all master data as CSV files in one zip, for safekeeping outside the database. */
  protected exportAll(): void {
    this.exporting.set(true);
    this.api.masterDataZip().subscribe({
      next: (blob) => {
        saveBlob(blob, `master-data-${new Date().toISOString().slice(0, 10)}.zip`);
        this.exporting.set(false);
      },
      error: async (e) => {
        this.exporting.set(false);
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 });
      },
    });
  }
  private readonly route = inject(ActivatedRoute);

  /** The tab named in ?tab= (the checklist links here), then wherever the user clicks. */
  private readonly tabFromUrl = toSignal(
    this.route.queryParamMap.pipe(map((p) => tabIndex(p.get('tab')))),
    { initialValue: 0 },
  );
  protected readonly selectedTab = linkedSignal(() => this.tabFromUrl());
}
