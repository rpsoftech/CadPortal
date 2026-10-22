import { Component, computed, effect, inject, signal, untracked } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { FixedPipe, StoneGramsPipe, caratsToGrams, fixed } from '../../core/format';
import { MATRIX_COLUMNS } from '../../core/matrix-csv';
import { StoneCode, StoneMatrixRow } from '../../core/models';
import { saveBlob } from '../../shared/download';
import {
  MatrixImportDialogComponent,
  MatrixRowDialogComponent,
  MatrixRowDialogData,
  StoneCodeDialogComponent,
} from './master-data-dialogs';
import { RecalcWatcher } from './recalc-watcher';

const SHOW_LIMIT = 300;

/** Quotes a CSV cell if it contains a delimiter, quote or newline. */
function cell(v: string | null): string {
  const s = v ?? '';
  return /[",\n\r\t]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

@Component({
  selector: 'app-matrix-tab',
  imports: [
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSelectModule,
    MatSlideToggleModule,
    FixedPipe,
    StoneGramsPipe,
  ],
  template: `
    <p class="muted">
      The weight of a stone (in grams, with its carats) and its price, per piece or per carat, for
      each (size, stone code). Gross weight uses the
      <strong>baseline</strong> stone code; flexible seats follow whichever code the planner picks.
    </p>

    <!-- Stone codes and the baseline -->
    <section>
      <h3>Stone codes</h3>
      <div class="row">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Baseline code</mat-label>
          <mat-select [value]="baseline()" (selectionChange)="pendingBaseline.set($event.value)">
            @for (c of activeCodes(); track c.id) {
              <mat-option [value]="c.code">{{ c.code }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <button
          matButton="outlined"
          (click)="saveBaseline()"
          [disabled]="!pendingBaseline() || pendingBaseline() === baseline() || busy()"
        >
          Set baseline
        </button>
        <span class="spacer"></span>
        <button matButton (click)="addCode()">Add stone code</button>
      </div>
      <div class="codes">
        @for (c of codes.value() ?? []; track c.id) {
          <mat-slide-toggle
            [checked]="c.active"
            [disabled]="c.is_default_baseline || busy()"
            (change)="toggleCode(c, $event.checked)"
          >
            <span class="mono">{{ c.code }}</span
            >{{ c.is_default_baseline ? ' (baseline)' : '' }}
          </mat-slide-toggle>
        }
      </div>
    </section>

    <!-- Gaps -->
    @if ((gaps.value() ?? []).length) {
      <section class="gaps" aria-live="polite">
        <h3>Missing for the baseline code</h3>
        <p class="muted">
          Seats use these sizes, but the baseline code has no row, so their gross weight is unknown.
        </p>
        <ul>
          @for (g of gaps.value() ?? []; track g.size_mm + g.stone_code) {
            <li>
              {{ g.size_mm | fixed: 2 }} mm × <span class="mono">{{ g.stone_code }}</span>
              <span class="muted">
                ({{ g.variation_count }} variation{{ g.variation_count === 1 ? '' : 's' }})</span
              >
              <button matButton (click)="addRow({ size_mm: g.size_mm, stone_code: g.stone_code })">
                Add
              </button>
            </li>
          }
        </ul>
      </section>
    }

    <!-- Rows -->
    <section>
      <div class="row">
        <h3>Rows</h3>
        <span class="spacer"></span>
        <button matButton (click)="exportCsv()" [disabled]="!(matrix.value() ?? []).length">
          Export CSV
        </button>
        <button matButton (click)="importCsv()">Import CSV / paste</button>
        <button matButton="filled" (click)="addRow()">Add row</button>
      </div>
      <div class="row">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Show code</mat-label>
          <mat-select [value]="filterCode()" (selectionChange)="filterCode.set($event.value)">
            <mat-option value="">All</mat-option>
            @for (c of codes.value() ?? []; track c.id) {
              <mat-option [value]="c.code">{{ c.code }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Size contains</mat-label>
          <input
            matInput
            inputmode="decimal"
            [value]="filterSize()"
            (input)="filterSize.set($any($event.target).value)"
          />
        </mat-form-field>
        <span class="muted"
          >{{ filtered().length }} of {{ (matrix.value() ?? []).length }} rows</span
        >
      </div>

      @if (matrix.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      }

      <div class="scroll">
        <table class="plain">
          <thead>
            <tr>
              <th>Size (mm)</th>
              <th>Code</th>
              <th>Sieve</th>
              <th class="num">Weight / stone (g)</th>
              <th class="num">Carats / stone</th>
              <th class="num">Price</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (r of shown(); track r.stone_code + r.size_mm) {
              <tr>
                <td>{{ r.size_mm | fixed: 2 }}</td>
                <td class="mono">{{ r.stone_code }}</td>
                <td>{{ r.sieve || '–' }}</td>
                <td class="num">{{ r.carats_per_stone | stoneGrams: 5 }}</td>
                <td class="num muted">{{ r.carats_per_stone | fixed: 4 }}</td>
                <td class="num">
                  @if (price(r); as p) {
                    {{ p | fixed: 2 }}
                    <span class="muted small">{{
                      r.rate_basis === 'ct' ? 'per ct' : 'per pc'
                    }}</span>
                  } @else {
                    <span class="unset"
                      >not set ({{ r.rate_basis === 'ct' ? 'per carat' : 'per piece' }})</span
                    >
                  }
                </td>
                <td class="actions">
                  <button matButton (click)="editRow(r)">Edit</button>
                  <button matButton (click)="deleteRow(r)" [disabled]="busy()">Delete</button>
                </td>
              </tr>
            } @empty {
              <tr>
                <td colspan="7" class="muted">No rows. Add one or import a CSV.</td>
              </tr>
            }
          </tbody>
        </table>
      </div>
      @if (filtered().length > shown().length) {
        <p class="muted">
          Showing the first {{ shown().length }}. Narrow the filters to see the rest.
        </p>
      }
    </section>
  `,
  styles: `
    section {
      margin: 16px 0;
    }
    h3 {
      margin: 0 0 8px;
    }
    .spacer {
      flex: 1;
    }
    .codes {
      display: flex;
      flex-wrap: wrap;
      gap: 16px;
      margin-top: 8px;
    }
    .gaps {
      padding: 8px 12px;
      border: 1px solid var(--mat-sys-outline-variant);
      border-radius: 8px;
    }
    .gaps ul {
      margin: 0;
      padding-left: 20px;
    }
    .scroll {
      overflow-x: auto;
    }
    .unset {
      color: var(--mat-sys-error);
    }
    .actions {
      white-space: nowrap;
    }
  `,
})
export class MatrixTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  private readonly recalc = inject(RecalcWatcher);

  private readonly tick = signal(0);
  protected readonly codes = rxResource({
    params: () => this.tick(),
    stream: () => this.api.stoneCodes(),
  });
  protected readonly matrix = rxResource({
    params: () => this.tick(),
    stream: () => this.api.stoneMatrix(),
  });
  protected readonly gaps = rxResource({
    params: () => this.tick(),
    stream: () => this.api.matrixGaps(),
  });

  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly pendingBaseline = signal('');
  protected readonly filterCode = signal('');
  protected readonly filterSize = signal('');

  protected readonly activeCodes = computed(() =>
    (this.codes.value() ?? []).filter((c) => c.active),
  );
  protected readonly baseline = computed(
    () => (this.codes.value() ?? []).find((c) => c.is_default_baseline)?.code ?? '',
  );

  protected readonly filtered = computed(() => {
    const code = this.filterCode();
    const size = this.filterSize().trim();
    return (this.matrix.value() ?? []).filter(
      (r) => (!code || r.stone_code === code) && (!size || fixed(r.size_mm, 2).startsWith(size)),
    );
  });
  protected readonly shown = computed(() => this.filtered().slice(0, SHOW_LIMIT));

  constructor() {
    // When the background recalculation finishes, refresh what depends on it.
    effect(() => {
      this.recalc.settled();
      untracked(() => this.gaps.reload());
    });
  }

  private changed(message: string, recalcQueued: boolean): void {
    this.snack.open(message, 'OK', { duration: 4000 });
    this.tick.update((n) => n + 1);
    if (recalcQueued) this.recalc.watch();
  }

  protected saveBaseline(): void {
    const code = this.pendingBaseline();
    if (!code) return;
    this.run(
      this.api.saveStoneMatrix([], code),
      `Baseline is now ${code}. Weights are being rebuilt.`,
      true,
    );
    this.pendingBaseline.set('');
  }

  protected toggleCode(c: StoneCode, active: boolean): void {
    this.run(
      this.api.setStoneCodeActive(c.id, active),
      `${c.code} ${active ? 'activated' : 'deactivated'}`,
      false,
    );
  }

  protected addCode(): void {
    this.dialog
      .open<StoneCodeDialogComponent, void, StoneCode>(StoneCodeDialogComponent, { width: '420px' })
      .afterClosed()
      .subscribe((c) => c && this.changed(`Added ${c.code}. Now add its sizes below.`, false));
  }

  protected addRow(prefill?: { size_mm: string; stone_code: string }): void {
    this.openRow({
      codes: this.activeCodes(),
      prefill: prefill && { ...prefill, size_mm: fixed(prefill.size_mm, 2) },
    });
  }

  protected editRow(row: StoneMatrixRow): void {
    this.openRow({ codes: this.codes.value() ?? [], row });
  }

  private openRow(data: MatrixRowDialogData): void {
    this.dialog
      .open<MatrixRowDialogComponent, MatrixRowDialogData, boolean>(MatrixRowDialogComponent, {
        data,
        width: '440px',
      })
      .afterClosed()
      .subscribe(
        (ok) =>
          ok &&
          this.changed('Row saved. Weights are being rebuilt if it affects the baseline.', true),
      );
  }

  protected deleteRow(r: StoneMatrixRow): void {
    const label = `${fixed(r.size_mm, 2)} mm × ${r.stone_code}`;
    if (!confirm(`Delete the row for ${label}?`)) return;
    this.run(this.api.deleteMatrixRow(r.size_mm, r.stone_code), `Deleted ${label}`, true);
  }

  protected importCsv(): void {
    this.dialog
      .open<MatrixImportDialogComponent, void, number>(MatrixImportDialogComponent, {
        width: '700px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((n) => n && this.changed(`Imported ${n} rows. Weights are being rebuilt.`, true));
  }

  /** The price costing uses for a row: per piece unless the row is priced per carat. */
  protected price(r: StoneMatrixRow): string | null {
    return r.rate_basis === 'ct' ? r.rate_per_ct : r.rate_per_pc;
  }

  protected exportCsv(): void {
    // The same eight columns the master-data zip writes, so either file can be imported again.
    const lines = [MATRIX_COLUMNS.join(',')];
    for (const r of this.matrix.value() ?? []) {
      lines.push(
        [
          fixed(r.size_mm, 2),
          r.stone_code,
          cell(r.sieve),
          fixed(r.carats_per_stone, 4),
          caratsToGrams(r.carats_per_stone, 5) ?? '',
          r.rate_basis,
          r.rate_per_pc ? fixed(r.rate_per_pc, 2) : '',
          r.rate_per_ct ? fixed(r.rate_per_ct, 2) : '',
        ].join(','),
      );
    }
    saveBlob(
      new Blob([lines.join(String.fromCharCode(10)) + String.fromCharCode(10)], {
        type: 'text/csv;charset=utf-8',
      }),
      'stone-matrix.csv',
    );
  }

  private run(
    req: ReturnType<ApiService['saveStoneMatrix']>,
    message: string,
    recalcQueued: boolean,
  ): void {
    this.busy.set(true);
    this.error.set('');
    req.subscribe({
      next: () => {
        this.busy.set(false);
        this.changed(message, recalcQueued);
      },
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
