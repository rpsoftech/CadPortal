import { Component, effect, input, output } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatRadioModule } from '@angular/material/radio';
import { MatSelectModule } from '@angular/material/select';
import { BaseInput, Metal, Variation } from '../core/models';
import { positiveDecimal } from './validators';

/**
 * The variation's base input: a volume, OR a reference weight in a reference alloy.
 * Emits the API payload, or null while the form is incomplete or invalid.
 */
@Component({
  selector: 'app-base-input',
  imports: [
    ReactiveFormsModule,
    MatRadioModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
  ],
  template: `
    <form [formGroup]="form" class="wrap">
      <mat-radio-group formControlName="mode" class="row" aria-label="Base input type">
        <mat-radio-button value="volume">Volume</mat-radio-button>
        <mat-radio-button value="reference">Reference weight + alloy</mat-radio-button>
      </mat-radio-group>

      @if (form.controls.mode.value === 'volume') {
        <mat-form-field appearance="outline">
          <mat-label>Volume (mm³)</mat-label>
          <input matInput formControlName="volume" inputmode="decimal" />
          @if (form.controls.volume.errors; as e) {
            <mat-error>{{ e['decimal'] || e['positive'] || 'Required' }}</mat-error>
          }
        </mat-form-field>
      } @else {
        <div class="row">
          <mat-form-field appearance="outline">
            <mat-label>Reference net weight (g)</mat-label>
            <input matInput formControlName="refWeight" inputmode="decimal" />
            @if (form.controls.refWeight.errors; as e) {
              <mat-error>{{ e['decimal'] || e['positive'] || 'Required' }}</mat-error>
            }
          </mat-form-field>
          <mat-form-field appearance="outline">
            <mat-label>Reference alloy</mat-label>
            <mat-select formControlName="refMetal">
              @for (m of metals(); track m.id) {
                <mat-option [value]="m.code">{{ m.code }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
        </div>
      }
    </form>
  `,
  styles: `
    .wrap {
      display: flex;
      flex-direction: column;
      gap: 4px;
    }
  `,
})
export class BaseInputComponent {
  readonly metals = input.required<Metal[]>();
  readonly initial = input<Variation | null>(null);
  readonly valueChange = output<BaseInput | null>();

  readonly form = new FormGroup({
    mode: new FormControl<'volume' | 'reference'>('volume', { nonNullable: true }),
    volume: new FormControl('', [Validators.required, positiveDecimal(3)]),
    refWeight: new FormControl('', [positiveDecimal(3)]),
    refMetal: new FormControl(''),
  });

  constructor() {
    this.form.controls.mode.valueChanges.subscribe((mode) => {
      const vol = this.form.controls.volume;
      const w = this.form.controls.refWeight;
      const m = this.form.controls.refMetal;
      if (mode === 'volume') {
        vol.addValidators(Validators.required);
        w.removeValidators(Validators.required);
        m.removeValidators(Validators.required);
      } else {
        vol.removeValidators(Validators.required);
        w.addValidators(Validators.required);
        m.addValidators(Validators.required);
      }
      [vol, w, m].forEach((c) => c.updateValueAndValidity({ emitEvent: false }));
      this.valueChange.emit(this.payload());
    });
    this.form.valueChanges.subscribe(() => this.valueChange.emit(this.payload()));

    // Load an existing variation's base input.
    effect(() => {
      const v = this.initial();
      if (!v) return;
      if (v.base_volume_mm3 !== null) {
        this.form.patchValue({ mode: 'volume', volume: v.base_volume_mm3 });
      } else {
        const code = this.metals().find((x) => x.id === v.base_ref_metal_id)?.code ?? '';
        this.form.patchValue({
          mode: 'reference',
          refWeight: v.base_ref_weight_g ?? '',
          refMetal: code,
        });
      }
    });
  }

  private payload(): BaseInput | null {
    if (this.form.invalid) return null;
    const v = this.form.getRawValue();
    return v.mode === 'volume'
      ? { volume_mm3: v.volume!.trim() }
      : { ref_weight_g: v.refWeight!.trim(), ref_metal_code: v.refMetal! };
  }
}
