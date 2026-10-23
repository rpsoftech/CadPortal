import { DatePipe } from '@angular/common';
import { Component, computed, inject, input, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router, RouterLink } from '@angular/router';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { ApiError, toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { BASIS_LABELS } from '../../core/matrix-price';
import { CostingSheetComparison, RateBasis } from '../../core/models';
import { CostProblemsComponent } from '../../shared/cost-problems.component';
import { saveBlob } from '../../shared/download';
import { positiveDecimal } from '../../shared/validators';

/** "+12.00" / "-3.50" / "0.00": the sign makes a change easy to see. */
export function signed(v: string | null, dp = 2): string {
  if (v === null) return '–';
  const f = fixed(v, dp);
  return /^-/.test(f) || /^0(\.0+)?$/.test(f) ? f : `+${f}`;
}

/** A saved costing sheet, read-only: exactly what was computed and the rates it used. */
@Component({
  selector: 'app-costing-sheet-detail',
  imports: [
    DatePipe,
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    CostProblemsComponent,
  ],
  template: `
    <div class="page">
      <p><a routerLink="/costing-sheets">← Costing sheets</a></p>
      @if (sheet.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (sheet.error()) {
        <div class="error-box" role="alert">This costing sheet could not be loaded. It may have been deleted.</div>
      }
      @if (sheet.value(); as s) {
        <div class="row head">
          <h1>{{ s.name }}</h1>
          <span class="spacer"></span>
          <button matButton="filled" (click)="download()" [disabled]="busy()">Download CSV</button>
          <button matButton (click)="recostOpen.set(!recostOpen())" [disabled]="busy()">Re-cost…</button>
          @if (canDelete(s.created_by)) {
            <button matButton (click)="remove()" [disabled]="busy()">Delete</button>
          }
        </div>
        <p class="muted">
          Saved {{ s.created_at | date: 'medium' }}
          @if (s.created_by_name) {
            by {{ s.created_by_name }}
          }
          @if (s.recost_of) {
            · re-cost of <a [routerLink]="['/costing-sheets', s.recost_of]">sheet {{ s.recost_of }}</a>
          }
        </p>
        <dl class="facts">
          <div><dt>Metal</dt><dd>{{ s.metal_code }}</dd></div>
          <div><dt>Stone code</dt><dd>{{ s.stone_code }}</dd></div>
          <div><dt>Gold rate / g</dt><dd>{{ fx(s.gold_rate_per_g, 2) }}</dd></div>
          <div><dt>Setting rate / pc</dt><dd>{{ fx(s.setting_rate_per_pc, 2) }}</dd></div>
          <div><dt>Designs</dt><dd>{{ s.design_count }}</dd></div>
          <div><dt>Variations</dt><dd>{{ s.variation_count }}</dd></div>
          <div><dt>Total</dt><dd class="strong">{{ fx(s.totals.total, 2) }}</dd></div>
        </dl>

        @if (error(); as e) {
          <div class="error-box" role="alert">
            {{ e.message }}
            @if (e.problems.length) {
              <app-cost-problems [problems]="e.problems" />
            }
          </div>
        }
        @if (busy()) {
          <mat-progress-bar mode="indeterminate" />
        }

        @if (recostOpen()) {
          <form [formGroup]="recost" class="row recost" (ngSubmit)="doRecost()" aria-label="Re-cost">
            <p class="muted full">
              Makes a new sheet from the same variations with today's wastage % and stone prices. The
              rates below default to this sheet's; this sheet is not changed.
            </p>
            <mat-form-field appearance="outline" class="field">
              <mat-label>Gold rate / g</mat-label>
              <input matInput formControlName="goldRate" inputmode="decimal" />
              <mat-hint>Empty keeps {{ fx(s.gold_rate_per_g, 2) }}</mat-hint>
            </mat-form-field>
            <mat-form-field appearance="outline" class="field">
              <mat-label>Setting rate / pc</mat-label>
              <input matInput formControlName="settingRate" inputmode="decimal" />
              <mat-hint>Empty keeps {{ fx(s.setting_rate_per_pc, 2) }}</mat-hint>
            </mat-form-field>
            <button matButton="filled" type="submit" [disabled]="recost.invalid || busy()">Re-cost</button>
          </form>
        }

        @if (comparison(); as c) {
          <section aria-label="Difference from the earlier sheet">
            <h2>Difference from <a [routerLink]="['/costing-sheets', c.previous_id]">sheet {{ c.previous_id }}</a></h2>
            <p>
              Total {{ fx(c.previous_total, 2) }} → {{ fx(c.total, 2) }}
              (<strong>{{ signed(c.difference) }}</strong>)
            </p>
            <table class="plain">
              <thead>
                <tr><th>Variation</th><th class="num">Before</th><th class="num">Now</th><th class="num">Change</th></tr>
              </thead>
              <tbody>
                @for (r of c.rows; track r.variation_id) {
                  <tr>
                    <td class="mono">{{ r.variation_code }}</td>
                    <td class="num">{{ r.previous_total === null ? 'not costed' : fx(r.previous_total, 2) }}</td>
                    <td class="num">{{ r.total === null ? 'not costed' : fx(r.total, 2) }}</td>
                    <td class="num">{{ signed(r.difference) }}</td>
                  </tr>
                }
              </tbody>
            </table>
          </section>
        }

        @if (s.problems.length) {
          <div class="warn" role="alert">
            <strong>{{ s.incomplete_count }} variation{{ s.incomplete_count === 1 ? '' : 's' }} not costed</strong>
            because data was missing when this sheet was made:
            <app-cost-problems [problems]="s.problems" />
          </div>
        }

        <h2>Costing</h2>
        <table class="plain" aria-label="Costed variations">
          <thead>
            <tr>
              <th>Design</th>
              <th>Variation</th>
              <th class="num">Net g</th>
              <th class="num">Wastage %</th>
              <th class="num">Gold g</th>
              <th class="num">Gold cost</th>
              <th class="num">Stone ct</th>
              <th class="num">Stone g</th>
              <th class="num">Stone cost</th>
              <th class="num">Pcs</th>
              <th class="num">Setting</th>
              <th class="num">Total</th>
            </tr>
          </thead>
          <tbody>
            @for (r of s.rows; track r.variation_id) {
              <tr>
                <td>
                  <span class="mono">{{ r.design_code }}</span>
                  @if (r.design_name) {
                    <span class="muted"> {{ r.design_name }}</span>
                  }
                </td>
                <td class="mono">{{ r.variation_code }}</td>
                <td class="num">{{ fx(r.net_g, 3) }}</td>
                <td class="num">{{ r.wastage_pct }}</td>
                <td class="num">{{ fx(r.gold_g, 3) }}</td>
                <td class="num">{{ fx(r.gold_cost, 2) }}</td>
                <td class="num">{{ fx(r.stone_ct, 4) }}</td>
                <td class="num">{{ fx(r.stone_g, 4) }}</td>
                <td class="num">{{ fx(r.stone_cost, 2) }}</td>
                <td class="num">{{ r.setting_pcs }}</td>
                <td class="num">{{ fx(r.setting_cost, 2) }}</td>
                <td class="num">{{ fx(r.total, 2) }}</td>
              </tr>
            }
            @if (s.rows.length > 1) {
              <tr class="total">
                <td colspan="2">Total</td>
                <td class="num">{{ fx(s.totals.net_g, 3) }}</td>
                <td></td>
                <td class="num">{{ fx(s.totals.gold_g, 3) }}</td>
                <td class="num">{{ fx(s.totals.gold_cost, 2) }}</td>
                <td class="num">{{ fx(s.totals.stone_ct, 4) }}</td>
                <td class="num">{{ fx(s.totals.stone_g, 4) }}</td>
                <td class="num">{{ fx(s.totals.stone_cost, 2) }}</td>
                <td class="num">{{ s.totals.setting_pcs }}</td>
                <td class="num">{{ fx(s.totals.setting_cost, 2) }}</td>
                <td class="num">{{ fx(s.totals.total, 2) }}</td>
              </tr>
            }
          </tbody>
        </table>

        <details>
          <summary>Rates used when this sheet was made</summary>
          <h3>Wastage %</h3>
          <ul>
            @for (m of s.rates.metals; track m.metal_code) {
              <li>{{ m.metal_code }}: {{ m.wastage_pct }} %</li>
            }
          </ul>
          <h3>Stone prices</h3>
          <table class="plain" aria-label="Stone prices used">
            <thead>
              <tr><th>Size mm</th><th>Code</th><th>Priced</th><th class="num">Price</th><th class="num">ct / stone</th></tr>
            </thead>
            <tbody>
              @for (p of s.rates.stone_rates; track p.size_mm + p.stone_code) {
                <tr>
                  <td>{{ fx(p.size_mm, 2) }}</td>
                  <td>{{ p.stone_code }}</td>
                  <td>{{ basisLabel(p.rate_basis) }}</td>
                  <td class="num">{{ fx(p.price, 2) }}</td>
                  <td class="num">{{ fx(p.carats_per_stone, 4) }}</td>
                </tr>
              }
            </tbody>
          </table>
        </details>
      }
    </div>
  `,
  styles: `
    h1 {
      margin: 0;
      overflow-wrap: anywhere;
    }
    .head {
      margin-bottom: 4px;
    }
    .spacer {
      flex: 1;
    }
    .facts {
      display: flex;
      flex-wrap: wrap;
      gap: 8px 24px;
      margin: 8px 0 16px;
    }
    .facts div {
      display: flex;
      flex-direction: column;
    }
    .facts dt {
      font-size: 0.8rem;
      color: var(--mat-sys-on-surface-variant);
    }
    .facts dd {
      margin: 0;
      font-variant-numeric: tabular-nums;
    }
    .strong {
      font-weight: 600;
    }
    .field {
      width: 170px;
    }
    .full {
      flex-basis: 100%;
      margin: 0;
    }
    .warn {
      border: 1px solid var(--mat-sys-tertiary);
      border-radius: 8px;
      padding: 8px 12px;
      margin: 8px 0;
    }
    details {
      margin-top: 16px;
    }
    @media (max-width: 720px) {
      .field {
        width: 100%;
      }
    }
  `,
})
export class CostingSheetDetailComponent {
  protected readonly basisLabel = (b: RateBasis): string => BASIS_LABELS[b].long;

  /** The route parameter, bound by withComponentInputBinding. */
  readonly id = input.required<string>();

  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly auth = inject(AuthService);

  protected readonly sheet = rxResource({
    params: () => Number(this.id()),
    stream: ({ params }) => this.api.costingSheet(params),
  });

  protected readonly busy = signal(false);
  protected readonly error = signal<ApiError | null>(null);
  protected readonly recostOpen = signal(false);
  /** The difference shown on a freshly re-costed sheet, kept with the id of the sheet it belongs to. */
  private readonly shown = signal<{ forId: number; cmp: CostingSheetComparison } | null>(null);
  protected readonly comparison = computed(() => {
    const c = this.shown();
    return c && c.forId === Number(this.id()) ? c.cmp : null;
  });

  protected readonly recost = new FormGroup({
    goldRate: new FormControl('', positiveDecimal(2)),
    settingRate: new FormControl('', positiveDecimal(2)),
  });

  protected readonly signed = signed;
  private readonly sheetValue = computed(() => this.sheet.value());

  protected fx(v: string, dp: number): string {
    return fixed(v, dp);
  }

  /** An admin may delete any sheet, a designer only their own (the server checks the same). */
  protected canDelete(createdBy: number | null): boolean {
    const u = this.auth.user();
    return !!u && (u.role === 'admin' || (createdBy !== null && u.id === createdBy));
  }

  protected download(): void {
    const s = this.sheetValue();
    if (!s) return;
    this.busy.set(true);
    this.error.set(null);
    this.api.costingSheetCsv(s.id).subscribe({
      next: (blob) => {
        saveBlob(blob, `costing-sheet-${s.id}.csv`);
        this.busy.set(false);
      },
      error: async (e) => {
        this.error.set(await toApiError(e));
        this.busy.set(false);
      },
    });
  }

  protected remove(): void {
    const s = this.sheetValue();
    if (!s) return;
    if (!confirm(`Delete the costing sheet “${s.name}”? This cannot be undone.`)) return;
    this.busy.set(true);
    this.error.set(null);
    this.api.deleteCostingSheet(s.id).subscribe({
      next: () => this.router.navigate(['/costing-sheets']),
      error: async (e) => {
        this.error.set(await toApiError(e));
        this.busy.set(false);
      },
    });
  }

  protected doRecost(): void {
    const s = this.sheetValue();
    if (!s || this.recost.invalid) return;
    const gold = (this.recost.controls.goldRate.value ?? '').trim();
    const setting = (this.recost.controls.settingRate.value ?? '').trim();
    this.busy.set(true);
    this.error.set(null);
    this.api
      .recostCostingSheet(s.id, {
        ...(gold ? { gold_rate_per_g: gold } : {}),
        ...(setting ? { setting_rate_per_pc: setting } : {}),
      })
      .subscribe({
        next: (r) => {
          this.busy.set(false);
          this.recostOpen.set(false);
          this.recost.reset();
          // Show the difference on the new sheet, which is where the user is taken.
          this.shown.set({ forId: r.sheet.id, cmp: r.comparison });
          this.router.navigate(['/costing-sheets', r.sheet.id]);
        },
        error: async (e) => {
          this.error.set(await toApiError(e));
          this.busy.set(false);
        },
      });
  }
}
