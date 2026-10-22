import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ApiService } from '../../core/api.service';
import { PREFIX_PATTERN } from '../../core/code-prefix';
import { toApiError } from '../../core/errors';
import { exactGrams, fixed } from '../../core/format';
import { parseMatrixCsv } from '../../core/matrix-csv';
import { weightToCarats } from '../../core/stone-units';
import {
  Category,
  Metal,
  MatrixRowInput,
  RateBasis,
  StoneCode,
  StoneMatrixRow,
  TAG_GROUPS,
  Tag,
  TagGroup,
} from '../../core/models';
import { percent, positiveDecimal } from '../../shared/validators';

const IMPORTS = [
  ReactiveFormsModule,
  MatDialogModule,
  MatButtonModule,
  MatFormFieldModule,
  MatInputModule,
  MatSelectModule,
  MatSlideToggleModule,
];

const COL = `
  .col { display: flex; flex-direction: column; gap: 4px; min-width: 360px; }
`;

// ------------------------------------------------------------------ metals

/** Creates an alloy, or edits SG, wastage %, default-input flag and active state. */
@Component({
  selector: 'app-metal-dialog',
  imports: IMPORTS,
  template: `
    <h2 mat-dialog-title>{{ data ? 'Edit ' + data.code : 'New alloy' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        @if (!data) {
          <mat-form-field appearance="outline">
            <mat-label>Code</mat-label>
            <input matInput formControlName="code" placeholder="18KW" class="mono" />
            <mat-hint>2 to 12 letters or digits</mat-hint>
          </mat-form-field>
        }
        <mat-form-field appearance="outline">
          <mat-label>Specific gravity</mat-label>
          <input matInput formControlName="sg" inputmode="decimal" />
          <mat-hint>Up to 4 decimals. Changing it rebuilds all weights.</mat-hint>
          @if (form.controls.sg.invalid && form.controls.sg.touched) {
            <mat-error>A positive number, up to 4 decimals</mat-error>
          }
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Wastage %</mat-label>
          <input matInput formControlName="wastage" inputmode="decimal" />
          <mat-hint>Needed for costing. Leave empty if not decided yet.</mat-hint>
          @if (form.controls.wastage.invalid) {
            <mat-error>0 to 100, up to 3 decimals</mat-error>
          }
        </mat-form-field>
        <mat-slide-toggle formControlName="isDefault">
          Default input / reference alloy
        </mat-slide-toggle>
        <mat-slide-toggle formControlName="active">Active (gets weights)</mat-slide-toggle>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Save</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: COL,
})
export class MetalDialogComponent {
  protected readonly data = inject<Metal | null>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<MetalDialogComponent, Metal>);
  private readonly api = inject(ApiService);
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected readonly form = new FormGroup({
    code: new FormControl('', {
      nonNullable: true,
      validators: this.data ? [] : [Validators.required, Validators.pattern(/^[A-Za-z0-9]{2,12}$/)],
    }),
    sg: new FormControl(this.data?.specific_gravity ?? '', {
      nonNullable: true,
      validators: [Validators.required, positiveDecimal(4)],
    }),
    wastage: new FormControl(this.data?.wastage_pct ?? '', {
      nonNullable: true,
      validators: [percent(3)],
    }),
    isDefault: new FormControl(this.data?.is_default_input ?? false, { nonNullable: true }),
    active: new FormControl(this.data?.active ?? true, { nonNullable: true }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    const body = {
      specific_gravity: v.sg.trim(),
      wastage_pct: v.wastage.trim() === '' ? null : v.wastage.trim(),
      is_default_input: v.isDefault,
      active: v.active,
    };
    this.busy.set(true);
    this.error.set('');
    const req = this.data
      ? this.api.updateMetal(this.data.id, body)
      : this.api.createMetal({ ...body, code: v.code.trim() });
    req.subscribe({
      next: (m) => this.ref.close(m),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

// ------------------------------------------------------------- matrix row

export interface MatrixRowDialogData {
  codes: StoneCode[];
  /** Editing an existing row: size and code are fixed. */
  row?: StoneMatrixRow;
  /** Starting values when adding (e.g. from a reported gap). */
  prefill?: { size_mm: string; stone_code: string };
}

/**
 * One row of the stone matrix: the weight of a stone of a size and code, and what it costs. The weight
 * is entered in grams (carats are the other choice) and the price per piece (per carat is the other
 * choice). Carats are what is stored, so grams are converted exactly and a weight that cannot be written
 * in 8 decimal places of carats is refused instead of rounded.
 */
@Component({
  selector: 'app-matrix-row-dialog',
  imports: IMPORTS,
  template: `
    <h2 mat-dialog-title>{{ data.row ? 'Edit row' : 'Add matrix row' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Size (mm)</mat-label>
          <input matInput formControlName="size" inputmode="decimal" />
          <mat-hint>Up to 2 decimals</mat-hint>
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Stone code</mat-label>
          <mat-select formControlName="code">
            @for (c of data.codes; track c.id) {
              <mat-option [value]="c.code">{{ c.code }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Sieve</mat-label>
          <input matInput formControlName="sieve" maxlength="20" />
        </mat-form-field>

        <div class="pair">
          <mat-form-field appearance="outline" class="grow">
            <mat-label>Weight per stone</mat-label>
            <input matInput formControlName="weight" inputmode="decimal" />
            <mat-hint>{{ weightHint() }}</mat-hint>
          </mat-form-field>
          <mat-form-field appearance="outline" class="unit">
            <mat-label>Unit</mat-label>
            <mat-select formControlName="weightUnit" aria-label="Weight unit">
              <mat-option value="g">grams</mat-option>
              <mat-option value="ct">carats</mat-option>
            </mat-select>
          </mat-form-field>
        </div>

        <div class="pair">
          <mat-form-field appearance="outline" class="grow">
            <mat-label>Price</mat-label>
            <input matInput formControlName="price" inputmode="decimal" />
            <mat-hint>Up to 2 decimals. Needed for costing.</mat-hint>
          </mat-form-field>
          <mat-form-field appearance="outline" class="unit">
            <mat-label>Priced</mat-label>
            <mat-select formControlName="basis" aria-label="Priced per">
              <mat-option value="pc">per piece</mat-option>
              <mat-option value="ct">per carat</mat-option>
            </mat-select>
          </mat-form-field>
        </div>
        @if (otherPriceNote(); as note) {
          <p class="muted small">{{ note }}</p>
        }
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Save</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    ${COL}
    .pair {
      display: flex;
      gap: 8px;
    }
    .grow {
      flex: 1;
    }
    .unit {
      width: 130px;
    }
  `,
})
export class MatrixRowDialogComponent {
  protected readonly data = inject<MatrixRowDialogData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<MatrixRowDialogComponent, boolean>);
  private readonly api = inject(ApiService);
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  private readonly row = this.data.row;
  private readonly startBasis: RateBasis = this.row?.rate_basis ?? 'pc';

  protected readonly form = new FormGroup({
    size: new FormControl(this.row?.size_mm ?? this.data.prefill?.size_mm ?? '', {
      nonNullable: true,
      validators: [Validators.required, positiveDecimal(2)],
    }),
    code: new FormControl(this.row?.stone_code ?? this.data.prefill?.stone_code ?? '', {
      nonNullable: true,
      validators: [Validators.required],
    }),
    sieve: new FormControl(this.row?.sieve ?? '', { nonNullable: true }),
    // An existing row is shown in grams, the unit this screen leads with.
    weight: new FormControl(this.row ? gramsText(this.row.carats_per_stone) : '', {
      nonNullable: true,
      validators: [Validators.required, Validators.pattern(/^\d+(\.\d+)?$/)],
    }),
    weightUnit: new FormControl<'g' | 'ct'>('g', { nonNullable: true }),
    price: new FormControl(
      (this.startBasis === 'ct' ? this.row?.rate_per_ct : this.row?.rate_per_pc) ?? '',
      { nonNullable: true, validators: [Validators.pattern(/^\d+(\.\d{1,2})?$/)] },
    ),
    basis: new FormControl<RateBasis>(this.startBasis, { nonNullable: true }),
  });

  private readonly values = toSignal(this.form.valueChanges, {
    initialValue: this.form.getRawValue(),
  });

  /** What the weight comes to in the other unit, or why it cannot be stored. */
  protected readonly weightHint = computed(() => {
    const v = this.form.getRawValue();
    void this.values();
    if (v.weight.trim() === '') {
      return v.weightUnit === 'g' ? 'e.g. 0.0014 g (= 0.0070 ct)' : 'Up to 8 decimals, e.g. 0.0070';
    }
    const w = weightToCarats(v.weight, v.weightUnit);
    if (!w.ok) return w.error;
    return v.weightUnit === 'g' ? `= ${fixed(w.carats, 4)} ct` : `= ${exactGrams(w.carats, 5)} g`;
  });

  /** When the other basis already has a price, say that it is kept but not used. */
  protected readonly otherPriceNote = computed(() => {
    void this.values();
    const basis = this.form.controls.basis.value;
    const other = basis === 'pc' ? this.row?.rate_per_ct : this.row?.rate_per_pc;
    return other
      ? `This row also holds ${other} ${basis === 'pc' ? 'per carat' : 'per piece'}; it is kept but not used for costing.`
      : '';
  });

  constructor() {
    if (this.row) {
      this.form.controls.size.disable();
      this.form.controls.code.disable();
    }
    // Choosing the other unit converts what is typed, so the number keeps its meaning.
    this.form.controls.weightUnit.valueChanges.subscribe((unit) => {
      const c = this.form.controls.weight;
      const was = unit === 'g' ? 'ct' : 'g';
      const w = weightToCarats(c.value, was);
      if (w.ok && c.value.trim() !== '') {
        c.setValue(unit === 'ct' ? w.carats : gramsText(w.carats));
      }
    });
  }

  protected save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    const w = weightToCarats(v.weight, v.weightUnit);
    if (!w.ok) {
      this.error.set(`Weight per stone: ${w.error}`);
      return;
    }
    const price = v.price.trim() === '' ? null : v.price.trim();
    // The chosen basis takes the typed price; the other basis keeps what the row already held.
    const row: MatrixRowInput = {
      size_mm: v.size.trim(),
      stone_code: v.code,
      sieve: v.sieve.trim(),
      carats_per_stone: w.carats,
      rate_per_pc: v.basis === 'pc' ? price : (this.row?.rate_per_pc ?? null),
      rate_per_ct: v.basis === 'ct' ? price : (this.row?.rate_per_ct ?? null),
      rate_basis: v.basis,
    };
    this.busy.set(true);
    this.error.set('');
    this.api.saveStoneMatrix([row]).subscribe({
      next: () => this.ref.close(true),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

// ------------------------------------------------------------ matrix import

@Component({
  selector: 'app-matrix-import-dialog',
  imports: [MatDialogModule, MatButtonModule],
  template: `
    <h2 mat-dialog-title>Import stone matrix</h2>
    <mat-dialog-content class="col wide">
      <p class="muted">
        Paste rows copied from a spreadsheet, or choose a CSV file with a header row, for example
        <span class="mono"
          >size_mm, stone_code, sieve, grams_per_stone, rate_per_pc, rate_per_ct, rate_basis</span
        >. Give the weight per stone as <span class="mono">grams_per_stone</span> or
        <span class="mono">carats_per_stone</span> (grams are converted exactly; a weight that would
        need rounding is refused). Stones are priced per piece unless
        <span class="mono">rate_basis</span> is <span class="mono">ct</span> (per carat). Columns
        may come in any order. Without a header the older five columns (size, code, sieve, carats,
        rate per carat) and the eight columns of Export CSV are read by position. Sieve and prices
        may be empty. Existing rows with the same size and code are updated; nothing is deleted.
        Stone codes must already exist.
      </p>
      <input type="file" accept=".csv,.tsv,.txt,text/csv" (change)="onFile($event)" />
      <textarea
        class="paste mono"
        rows="8"
        spellcheck="false"
        placeholder="1.10,2HD,+0000,0.0070,50000"
        [value]="text()"
        (input)="text.set($any($event.target).value)"
        aria-label="Matrix data"
      ></textarea>

      @if (text().trim()) {
        <div [class.error-box]="parsed().errors.length" [class.ok]="!parsed().errors.length">
          {{ parsed().rows.length }} valid row{{ parsed().rows.length === 1 ? '' : 's'
          }}{{
            parsed().errors.length
              ? ', ' +
                parsed().errors.length +
                ' problem' +
                (parsed().errors.length === 1 ? '' : 's')
              : ''
          }}
        </div>
        @if (parsed().errors.length) {
          <ul class="errors">
            @for (e of shownErrors(); track e) {
              <li>{{ e }}</li>
            }
          </ul>
          @if (parsed().errors.length > shownErrors().length) {
            <p class="muted">…and {{ parsed().errors.length - shownErrors().length }} more.</p>
          }
          <p class="muted">Fix the lines above to import. Nothing is imported partially.</p>
        }
      }
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" (click)="save()" [disabled]="!canImport() || busy()">
        Import {{ parsed().rows.length }} rows
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .wide {
      min-width: min(640px, 80vw);
    }
    .paste {
      width: 100%;
      box-sizing: border-box;
      padding: 8px;
      border-radius: 8px;
      border: 1px solid var(--mat-sys-outline);
      background: transparent;
      color: inherit;
    }
    .ok {
      color: var(--mat-sys-primary);
    }
    .errors {
      margin: 0;
      padding-left: 20px;
      max-height: 160px;
      overflow: auto;
    }
  `,
})
export class MatrixImportDialogComponent {
  private readonly ref = inject(MatDialogRef<MatrixImportDialogComponent, number>);
  private readonly api = inject(ApiService);

  protected readonly text = signal('');
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly parsed = computed(() => parseMatrixCsv(this.text()));
  protected readonly shownErrors = computed(() => this.parsed().errors.slice(0, 15));
  protected readonly canImport = computed(
    () => this.parsed().rows.length > 0 && this.parsed().errors.length === 0,
  );

  protected onFile(ev: Event): void {
    const file = (ev.target as HTMLInputElement).files?.[0];
    if (!file) return;
    if (file.size > 2_000_000) {
      this.error.set('That file is too large for a stone matrix.');
      return;
    }
    this.error.set('');
    void file.text().then((t) => this.text.set(t));
  }

  protected save(): void {
    if (!this.canImport()) return;
    const rows = this.parsed().rows;
    this.busy.set(true);
    this.error.set('');
    this.api.saveStoneMatrix(rows).subscribe({
      next: () => this.ref.close(rows.length),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

// ---------------------------------------------------- categories, tags, codes

@Component({
  selector: 'app-category-dialog',
  imports: IMPORTS,
  template: `
    <h2 mat-dialog-title>{{ data ? 'Edit category' : 'New category' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" maxlength="60" />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Code prefix</mat-label>
          <input
            matInput
            formControlName="prefix"
            maxlength="6"
            class="mono"
            style="text-transform: uppercase"
            autocomplete="off"
          />
          <mat-hint
            >1 to 6 letters, e.g. LR: designs become LR0001, LR0002… A change affects new designs
            only.</mat-hint
          >
          @if (form.controls.prefix.invalid) {
            <mat-error>Letters only, 1 to 6</mat-error>
          }
        </mat-form-field>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Save</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: COL,
})
export class CategoryDialogComponent {
  protected readonly data = inject<Category | null>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<CategoryDialogComponent, Category>);
  private readonly api = inject(ApiService);
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly form = new FormGroup({
    name: new FormControl(this.data?.name ?? '', {
      nonNullable: true,
      validators: [Validators.required],
    }),
    prefix: new FormControl(this.data?.code_prefix ?? '', {
      nonNullable: true,
      validators: [Validators.pattern(PREFIX_PATTERN)],
    }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    const body = { name: v.name.trim(), code_prefix: v.prefix.trim().toUpperCase() };
    this.busy.set(true);
    this.error.set('');
    const req = this.data
      ? this.api.updateCategory(this.data.id, body)
      : this.api.createCategory(body);
    req.subscribe({
      next: (c) => this.ref.close(c),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

@Component({
  selector: 'app-tag-dialog',
  imports: IMPORTS,
  template: `
    <h2 mat-dialog-title>{{ data ? 'Edit tag' : 'New tag' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" maxlength="40" />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Group</mat-label>
          <mat-select formControlName="group">
            @for (g of groups; track g) {
              <mat-option [value]="g">{{ g }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        @if (data) {
          <mat-slide-toggle formControlName="active">
            Active (deactivated tags disappear from filters)
          </mat-slide-toggle>
        }
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Save</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: COL,
})
export class TagDialogComponent {
  protected readonly data = inject<Tag | null>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<TagDialogComponent, Tag>);
  private readonly api = inject(ApiService);
  protected readonly groups = TAG_GROUPS;
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly form = new FormGroup({
    name: new FormControl(this.data?.name ?? '', {
      nonNullable: true,
      validators: [Validators.required],
    }),
    group: new FormControl<TagGroup>(this.data?.tag_group ?? 'Motif', { nonNullable: true }),
    active: new FormControl(this.data?.active ?? true, { nonNullable: true }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    const req = this.data
      ? this.api.updateTag(this.data.id, {
          name: v.name.trim(),
          tag_group: v.group,
          active: v.active,
        })
      : this.api.createTag({ name: v.name.trim(), tag_group: v.group });
    req.subscribe({
      next: (t) => this.ref.close(t),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

@Component({
  selector: 'app-stone-code-dialog',
  imports: IMPORTS,
  template: `
    <h2 mat-dialog-title>New stone code</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Code</mat-label>
          <input matInput formControlName="code" placeholder="3HD" class="mono" />
          <mat-hint>1 to 12 letters or digits. Add its sizes in the matrix afterwards.</mat-hint>
        </mat-form-field>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Add</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: COL,
})
export class StoneCodeDialogComponent {
  private readonly ref = inject(MatDialogRef<StoneCodeDialogComponent, StoneCode>);
  private readonly api = inject(ApiService);
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly form = new FormGroup({
    code: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.pattern(/^[A-Za-z0-9]{1,12}$/)],
    }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    this.busy.set(true);
    this.error.set('');
    this.api.createStoneCode(this.form.getRawValue().code.trim()).subscribe({
      next: (c) => this.ref.close(c),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

/** A weight in carats as grams to type into a box: exact, without trailing zeros (0.2, not 0.20000). */
function gramsText(carats: string): string {
  return exactGrams(carats, 0) ?? '';
}
