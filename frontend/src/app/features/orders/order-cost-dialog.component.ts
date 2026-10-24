import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ApiService } from '../../core/api.service';
import { ApiError, toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { CostOrderResult, Order } from '../../core/models';
import { CostProblemsComponent } from '../../shared/cost-problems.component';
import { positiveDecimal } from '../../shared/validators';

export interface OrderCostData {
  order: Order;
}

/**
 * Costs an order from today's master data for its quantities and saves an immutable costing sheet linked
 * to it. Missing data is listed (grouped by design) and nothing is saved. Closes with the sheet id when the
 * person opens it, otherwise with nothing.
 */
@Component({
  selector: 'app-order-cost-dialog',
  imports: [
    ReactiveFormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSlideToggleModule,
    CostProblemsComponent,
  ],
  template: `
    <h2 mat-dialog-title>Cost {{ data.order.number }}</h2>
    @if (!result()) {
      <form [formGroup]="form" (ngSubmit)="cost()" aria-label="Costing inputs">
        <mat-dialog-content class="col">
          <p class="muted">
            Costs every line for its quantity in {{ data.order.metal_code }} · {{ data.order.stone_code }},
            with the wastage % and stone prices in master data <strong>today</strong>, and saves the result
            as a costing sheet that never changes afterwards.
          </p>
          <div class="row">
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
          </div>
          <mat-slide-toggle formControlName="skip">
            Skip lines with missing data (they are listed, not costed)
          </mat-slide-toggle>
          @if (busy()) {
            <mat-progress-bar mode="indeterminate" aria-label="Costing the order" />
          }
          @if (error(); as e) {
            <div class="error-box" role="alert">
              {{ e.message }}
              @if (e.problems.length) {
                <app-cost-problems [problems]="e.problems" />
              }
            </div>
          }
        </mat-dialog-content>
        <mat-dialog-actions align="end">
          <button matButton type="button" mat-dialog-close>Cancel</button>
          <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Cost this order</button>
        </mat-dialog-actions>
      </form>
    } @else {
      <mat-dialog-content class="col">
        @if (result(); as r) {
          <p class="ok" role="status">
            Saved as “{{ r.sheet.name }}”: total {{ fx(r.sheet.totals.total) }} for
            {{ r.sheet.totals.variations }} line{{ r.sheet.totals.variations === 1 ? '' : 's' }}.
          </p>
          @if (r.snapshot_differs) {
            <div class="warn" role="alert">
              <strong>This order no longer matches the designs.</strong> {{ r.drift.changed }} line{{
                r.drift.changed === 1 ? ' has' : 's have'
              }}
              changed and {{ r.drift.unavailable }} {{ r.drift.unavailable === 1 ? 'is' : 'are' }} no longer
              available since the order was made. The costing used the designs as they are today; production
              builds what the order says.
            </div>
          }
          @if (r.sheet.problems.length) {
            <div class="warn" role="alert">
              Not costed (data missing):
              <app-cost-problems [problems]="r.sheet.problems" />
            </div>
          }
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Close</button>
        <button matButton="filled" type="button" (click)="open()">Open the sheet</button>
      </mat-dialog-actions>
    }
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .field {
      width: 170px;
    }
    .warn {
      padding: 8px 12px;
      border-radius: 8px;
      background: var(--mat-sys-tertiary-container);
      color: var(--mat-sys-on-tertiary-container);
    }
    .ok {
      color: var(--mat-sys-primary);
    }
    @media (max-width: 720px) {
      .field {
        width: 100%;
      }
    }
  `,
})
export class OrderCostDialogComponent {
  protected readonly data = inject<OrderCostData>(MAT_DIALOG_DATA);
  private readonly ref = inject<MatDialogRef<OrderCostDialogComponent, number>>(MatDialogRef);
  private readonly api = inject(ApiService);

  protected readonly form = new FormGroup({
    goldRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    settingRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    skip: new FormControl(false, { nonNullable: true }),
  });
  protected readonly busy = signal(false);
  protected readonly error = signal<ApiError | null>(null);
  protected readonly result = signal<CostOrderResult | null>(null);

  protected fx(v: string): string {
    return fixed(v, 2);
  }

  protected cost(): void {
    if (this.form.invalid) return;
    const f = this.form.controls;
    this.busy.set(true);
    this.error.set(null);
    this.api
      .costOrder(this.data.order.id, {
        gold_rate_per_g: (f.goldRate.value ?? '').trim(),
        setting_rate_per_pc: (f.settingRate.value ?? '').trim(),
        skip_incomplete: f.skip.value,
      })
      .subscribe({
        next: (r) => {
          this.result.set(r);
          this.busy.set(false);
        },
        error: async (e) => {
          this.error.set(await toApiError(e));
          this.busy.set(false);
        },
      });
  }

  protected open(): void {
    const r = this.result();
    if (r) this.ref.close(r.sheet.id);
  }
}
