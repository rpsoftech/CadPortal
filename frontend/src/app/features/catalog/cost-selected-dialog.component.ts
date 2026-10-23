import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { catchError, map, of, shareReplay } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { SelectedDesign } from '../../core/catalog-selection';
import { ApiError, toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { CostPreview, CostSheetRequest } from '../../core/models';
import { CostProblemsComponent } from '../../shared/cost-problems.component';
import { saveBlob } from '../../shared/download';
import { positiveDecimal } from '../../shared/validators';

export interface CostSelectedData {
  designs: SelectedDesign[];
}

/**
 * Whether a previewed costing may be downloaded or saved: there must be something costed, and if
 * anything is incomplete the user must have chosen to skip it (nothing is dropped silently).
 */
export function costUsable(r: CostPreview | null, skip: boolean): boolean {
  return !!r && r.rows.length > 0 && (r.problems.length === 0 || skip);
}

/** Closes with the id of the costing sheet if one was saved, otherwise with nothing. */
@Component({
  selector: 'app-cost-selected-dialog',
  imports: [
    ReactiveFormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSelectModule,
    MatSlideToggleModule,
    CostProblemsComponent,
  ],
  template: `
    <h2 mat-dialog-title>
      Cost {{ data.designs.length }} design{{ data.designs.length === 1 ? '' : 's' }}
    </h2>
    <mat-dialog-content class="col">
      <p class="muted">
        Every variation of the selected designs is costed in one metal and one stone code, with the
        wastage % and stone prices from master data.
      </p>
      <form [formGroup]="form" class="row" (ngSubmit)="preview()" aria-label="Costing inputs">
        <mat-form-field appearance="outline" class="field">
          <mat-label>Metal</mat-label>
          <mat-select formControlName="metal">
            @for (m of metals(); track m) {
              <mat-option [value]="m">{{ m }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline" class="field">
          <mat-label>Stone code</mat-label>
          <mat-select formControlName="stone">
            @for (s of stones(); track s) {
              <mat-option [value]="s">{{ s }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline" class="field">
          <mat-label>Gold rate / g</mat-label>
          <input matInput formControlName="goldRate" inputmode="decimal" />
          @if (form.controls.goldRate.errors; as e) {
            <mat-error>{{ e['required'] ? 'Required' : (e['decimal'] ?? e['positive']) }}</mat-error>
          }
        </mat-form-field>
        <mat-form-field appearance="outline" class="field">
          <mat-label>Setting rate / pc</mat-label>
          <input matInput formControlName="settingRate" inputmode="decimal" />
          @if (form.controls.settingRate.errors; as e) {
            <mat-error>{{ e['required'] ? 'Required' : (e['decimal'] ?? e['positive']) }}</mat-error>
          }
        </mat-form-field>
        <mat-slide-toggle formControlName="skip">
          Skip variations with missing data (they are listed, not costed)
        </mat-slide-toggle>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Preview</button>
      </form>

      @if (busy()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (error(); as e) {
        <div class="error-box" role="alert">
          {{ e.message }}
          @if (e.problems.length) {
            <app-cost-problems [problems]="e.problems" />
          }
        </div>
      }

      @if (result(); as r) {
        @if (r.problems.length) {
          <div class="warn" role="alert">
            <strong>Data is missing for {{ incomplete() }} variation{{ incomplete() === 1 ? '' : 's' }}.</strong>
            @if (form.controls.skip.value) {
              They are left out of the costing and listed at the end of the CSV.
            } @else {
              Nothing is costed until this is fixed in master data, or you turn on “Skip variations
              with missing data”.
            }
            <app-cost-problems [problems]="r.problems" />
          </div>
        }
        @if (r.rows.length) {
          <table class="plain" aria-label="Costing preview">
            <thead>
              <tr>
                <th>Design</th>
                <th>Code</th>
                <th>Variation</th>
                <th class="num">Net g</th>
                <th class="num">Gross g</th>
                <th class="num">Stone g</th>
                <th class="num">Total</th>
              </tr>
            </thead>
            <tbody>
              @for (row of r.rows; track row.variation_id) {
                <tr>
                  <td>{{ row.design_name || '–' }}</td>
                  <td class="mono">{{ row.design_code }}</td>
                  <td class="mono">{{ row.variation_code }}</td>
                  <td class="num">{{ fx(row.net_g, 3) }}</td>
                  <td class="num">{{ fx(row.gross_g, 3) }}</td>
                  <td class="num">{{ fx(row.stone_g, 4) }}</td>
                  <td class="num">{{ fx(row.total, 2) }}</td>
                </tr>
              }
              @if (r.rows.length > 1) {
                <tr class="total">
                  <td colspan="3">Total ({{ r.totals.variations }} variations)</td>
                  <td class="num">{{ fx(r.totals.net_g, 3) }}</td>
                  <td class="num">{{ fx(r.totals.gross_g, 3) }}</td>
                  <td class="num">{{ fx(r.totals.stone_g, 4) }}</td>
                  <td class="num">{{ fx(r.totals.total, 2) }}</td>
                </tr>
              }
            </tbody>
          </table>

          <form
            [formGroup]="saveForm"
            class="row save"
            (ngSubmit)="save()"
            aria-label="Save as costing sheet"
          >
            @if (downloaded()) {
              <p class="muted full">Downloaded. Keep this costing so it can be opened again later:</p>
            }
            <mat-form-field appearance="outline" class="name">
              <mat-label>Costing sheet name</mat-label>
              <input matInput formControlName="name" maxlength="120" autocomplete="off" />
            </mat-form-field>
            <button matButton type="submit" [disabled]="saveForm.invalid || busy() || !canUse()">
              Save as costing sheet
            </button>
          </form>
        }
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Close</button>
      <button matButton="filled" (click)="download()" [disabled]="busy() || !canUse()">
        Download CSV
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
      min-width: min(780px, 80vw);
    }
    .field {
      width: 170px;
    }
    .name {
      flex: 1;
      min-width: 220px;
    }
    .full {
      flex-basis: 100%;
      margin: 0;
    }
    .warn {
      border: 1px solid var(--mat-sys-tertiary);
      border-radius: 8px;
      padding: 8px 12px;
    }
    table.plain {
      font-size: 0.9rem;
      /* a block table (phone width) would otherwise be squeezed to nothing in the flex column */
      flex: none;
    }
    @media (max-width: 720px) {
      .col {
        min-width: 0;
      }
      .field {
        width: 100%;
      }
    }
  `,
})
export class CostSelectedDialogComponent {
  protected readonly data = inject<CostSelectedData>(MAT_DIALOG_DATA);
  private readonly ref = inject<MatDialogRef<CostSelectedDialogComponent, number>>(MatDialogRef);
  private readonly api = inject(ApiService);

  private readonly activeMetals$ = this.api.metals().pipe(
    map((ms) => ms.filter((m) => m.active).map((m) => m.code)),
    catchError(() => of([] as string[])),
    shareReplay(1),
  );
  private readonly activeStones$ = this.api.stoneCodes().pipe(
    map((ss) => ss.filter((s) => s.active).map((s) => s.code)),
    catchError(() => of([] as string[])),
    shareReplay(1),
  );
  protected readonly metals = toSignal(this.activeMetals$, { initialValue: [] as string[] });
  protected readonly stones = toSignal(this.activeStones$, { initialValue: [] as string[] });

  protected readonly form = new FormGroup({
    metal: new FormControl('', { nonNullable: true, validators: Validators.required }),
    stone: new FormControl('', { nonNullable: true, validators: Validators.required }),
    goldRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    settingRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    skip: new FormControl(false, { nonNullable: true }),
  });
  protected readonly saveForm = new FormGroup({
    name: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.maxLength(120), Validators.pattern(/\S/)],
    }),
  });

  protected readonly busy = signal(false);
  protected readonly error = signal<ApiError | null>(null);
  protected readonly result = signal<CostPreview | null>(null);
  protected readonly downloaded = signal(false);
  /** The inputs the current preview was made with. */
  private previewed: CostSheetRequest | null = null;

  protected readonly incomplete = computed(() =>
    (this.result()?.problems ?? []).reduce((n, d) => n + Math.max(d.variations.length, 1), 0),
  );

  constructor() {
    // Changing a rate, metal or stone code makes the preview stale; the skip toggle only changes
    // what may be downloaded, not the rows.
    this.form.valueChanges.subscribe(() => {
      if (this.previewed && !this.sameInputs()) {
        this.result.set(null);
        this.previewed = null;
      }
      this.downloaded.set(false);
    });
    // With a single active metal / stone code there is nothing to choose.
    this.activeMetals$.subscribe((c) => this.pickOnly(this.form.controls.metal, c));
    this.activeStones$.subscribe((c) => this.pickOnly(this.form.controls.stone, c));
  }

  private pickOnly(ctl: FormControl<string>, codes: string[]): void {
    if (!ctl.value && codes.length === 1) ctl.setValue(codes[0]);
  }

  protected fx(v: string | null, dp: number): string {
    return fixed(v, dp);
  }

  private request(): CostSheetRequest {
    const f = this.form.controls;
    return {
      design_ids: this.data.designs.map((d) => d.id),
      metal_code: f.metal.value,
      stone_code: f.stone.value,
      gold_rate_per_g: (f.goldRate.value ?? '').trim(),
      setting_rate_per_pc: (f.settingRate.value ?? '').trim(),
      skip_incomplete: f.skip.value,
    };
  }

  private sameInputs(): boolean {
    const a = this.request();
    const b = this.previewed!;
    return (
      a.metal_code === b.metal_code &&
      a.stone_code === b.stone_code &&
      a.gold_rate_per_g === b.gold_rate_per_g &&
      a.setting_rate_per_pc === b.setting_rate_per_pc
    );
  }

  /** The CSV and the sheet may only be made from the previewed inputs, and only when allowed. */
  protected canUse(): boolean {
    return (
      !!this.previewed && !this.form.invalid && costUsable(this.result(), this.form.controls.skip.value)
    );
  }

  protected preview(): void {
    if (this.form.invalid) return;
    const req = this.request();
    this.busy.set(true);
    this.error.set(null);
    this.result.set(null);
    this.api.costingPreview(req).subscribe({
      next: (r) => {
        this.previewed = req;
        this.result.set(r);
        this.busy.set(false);
      },
      error: async (e) => {
        this.error.set(await toApiError(e));
        this.busy.set(false);
      },
    });
  }

  protected download(): void {
    if (!this.canUse()) return;
    this.busy.set(true);
    this.error.set(null);
    this.api.costingSelectionCsv(this.request()).subscribe({
      next: (blob) => {
        saveBlob(blob, 'costing.csv');
        this.downloaded.set(true);
        this.busy.set(false);
      },
      error: async (e) => {
        this.error.set(await toApiError(e));
        this.busy.set(false);
      },
    });
  }

  protected save(): void {
    if (!this.canUse() || this.saveForm.invalid) return;
    this.busy.set(true);
    this.error.set(null);
    this.api
      .createCostingSheet({ ...this.request(), name: this.saveForm.controls.name.value.trim() })
      .subscribe({
        next: (sheet) => this.ref.close(sheet.id),
        error: async (e) => {
          this.error.set(await toApiError(e));
          this.busy.set(false);
        },
      });
  }
}
