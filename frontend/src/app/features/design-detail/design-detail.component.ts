import { Component, computed, inject, input, linkedSignal, signal } from '@angular/core';
import { rxResource, toObservable, toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatRadioModule } from '@angular/material/radio';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router, RouterLink } from '@angular/router';
import { catchError, from, map, of, startWith, switchMap } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { ApiError, toApiError } from '../../core/errors';
import { FixedPipe } from '../../core/format';
import { BomResult, CostingItem, Variation } from '../../core/models';
import { saveBlob } from '../../shared/download';
import { NewDesignData, NewDesignDialogComponent } from '../../shared/new-design-dialog.component';
import {
  NewVariationData,
  NewVariationDialogComponent,
} from '../../shared/new-variation-dialog.component';
import { positiveDecimal } from '../../shared/validators';

export type BomState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ok'; bom: BomResult }
  | { status: 'error'; error: ApiError };

@Component({
  selector: 'app-design-detail',
  imports: [
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatButtonToggleModule,
    MatCardModule,
    MatChipsModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatRadioModule,
    MatSelectModule,
    FixedPipe,
  ],
  templateUrl: './design-detail.component.html',
  styleUrl: './design-detail.component.scss',
})
export class DesignDetailComponent {
  /** Route param, bound by withComponentInputBinding(). */
  readonly code = input.required<string>();

  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly router = inject(Router);
  private readonly snack = inject(MatSnackBar);
  protected readonly auth = inject(AuthService);

  private readonly reloadTick = signal(0);

  protected readonly design = rxResource({
    params: () => ({ code: this.code(), tick: this.reloadTick() }),
    stream: ({ params }) => this.api.getDesign(params.code),
  });
  protected readonly designError = computed(() => {
    const e = this.design.error();
    return e
      ? ((e as { error?: { error?: string } }).error?.error ?? 'Could not load this design')
      : '';
  });

  protected readonly metals = toSignal(
    this.api.metals().pipe(
      map((ms) => ms.filter((m) => m.active)),
      catchError(() => of([])),
    ),
    { initialValue: [] },
  );
  private readonly tagPool = toSignal(this.api.tags().pipe(catchError(() => of([]))), {
    initialValue: [],
  });
  private readonly categories = toSignal(this.api.categories().pipe(catchError(() => of([]))), {
    initialValue: [],
  });
  protected readonly categoryName = computed(
    () => this.categories().find((c) => c.id === this.design.value()?.category_id)?.name ?? '',
  );
  protected readonly stoneCodes = toSignal(
    this.api.stoneCodes().pipe(
      map((cs) => cs.filter((c) => c.active)),
      catchError(() => of([])),
    ),
    { initialValue: [] },
  );

  // ----- selections (keep the user's choice across reloads when still valid) -----
  protected readonly variationId = linkedSignal<Variation[] | undefined, number | null>({
    source: () => this.design.value()?.variations,
    computation: (vs, prev) =>
      vs?.some((v) => v.id === prev?.value) ? prev!.value : (vs?.[0]?.id ?? null),
  });
  protected readonly stoneCode = linkedSignal<{ code: string; baseline: boolean }[], string>({
    source: () => this.stoneCodes().map((c) => ({ code: c.code, baseline: c.is_default_baseline })),
    computation: (cs, prev) =>
      cs.some((c) => c.code === prev?.value)
        ? prev!.value
        : (cs.find((c) => c.baseline)?.code ?? cs[0]?.code ?? ''),
  });
  protected readonly metalCode = linkedSignal<{ code: string; def: boolean }[], string>({
    source: () => this.metals().map((m) => ({ code: m.code, def: m.is_default_input })),
    computation: (ms, prev) =>
      ms.some((m) => m.code === prev?.value)
        ? prev!.value
        : (ms.find((m) => m.def)?.code ?? ms[0]?.code ?? ''),
  });

  protected readonly selected = computed<Variation | null>(
    () => this.design.value()?.variations.find((v) => v.id === this.variationId()) ?? null,
  );

  // ----- weight matrix: metals x variations -----
  protected readonly matrixMetals = computed(() => {
    const set = new Set<string>();
    for (const v of this.design.value()?.variations ?? []) {
      for (const w of v.weights) set.add(w.metal_code);
    }
    return [...set].sort();
  });

  protected hasGap(v: Variation): boolean {
    return v.weights.some((w) => w.gross_weight_g === null);
  }

  protected cell(v: Variation, metal: string): { net: string; gross: string } | null {
    const w = v.weights.find((x) => x.metal_code === metal);
    return w ? { net: w.net_weight_g, gross: w.gross_weight_g ?? '–' } : null;
  }

  // ----- live BOM simulator: server math, re-run whenever a selection changes -----
  protected readonly bomState = toSignal(
    toObservable(computed(() => ({ id: this.variationId(), code: this.stoneCode() }))).pipe(
      switchMap(({ id, code }) =>
        id === null || !code
          ? of<BomState>({ status: 'idle' })
          : this.api.simulateBom(id, code).pipe(
              map((bom): BomState => ({ status: 'ok', bom })),
              startWith<BomState>({ status: 'loading' }),
              catchError((e) =>
                from(toApiError(e)).pipe(map((error): BomState => ({ status: 'error', error }))),
              ),
            ),
      ),
    ),
    { initialValue: { status: 'idle' } as BomState },
  );

  protected readonly bom = computed(() => {
    const s = this.bomState();
    return s.status === 'ok' ? s.bom : null;
  });
  protected readonly bomError = computed(() => {
    const s = this.bomState();
    return s.status === 'error' ? s.error : null;
  });
  protected readonly metalTotals = computed(
    () => this.bom()?.metals.find((m) => m.metal_code === this.metalCode()) ?? null,
  );

  // ----- exports -----
  protected readonly costing = new FormGroup({
    goldRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    settingRate: new FormControl('', [Validators.required, positiveDecimal(2)]),
    scope: new FormControl<'selected' | 'all'>('selected', { nonNullable: true }),
  });
  protected readonly exportBusy = signal(false);
  protected readonly exportError = signal<ApiError | null>(null);

  protected readonly canExport = computed(
    () => this.selected() !== null && !!this.metalCode() && !!this.stoneCode(),
  );

  protected jobBag(): void {
    const v = this.selected();
    if (!v) return;
    this.runExport(
      this.api.jobBagPdf({
        variation_id: v.id,
        metal_code: this.metalCode(),
        stone_code: this.stoneCode(),
      }),
      `jobbag-${v.code}.pdf`,
    );
  }

  protected costingCsv(): void {
    const d = this.design.value();
    if (!d || this.costing.invalid) return;
    const vars = this.costing.controls.scope.value === 'all' ? d.variations : [this.selected()!];
    const items: CostingItem[] = vars
      .filter((v) => !!v)
      .map((v) => ({
        variation_id: v.id,
        metal_code: this.metalCode(),
        stone_code: this.stoneCode(),
      }));
    this.runExport(
      this.api.costingCsv({
        items,
        gold_rate_per_g: this.costing.controls.goldRate.value!.trim(),
        setting_rate_per_pc: this.costing.controls.settingRate.value!.trim(),
      }),
      `costing-${d.code}.csv`,
    );
  }

  private runExport(req: ReturnType<ApiService['jobBagPdf']>, filename: string): void {
    this.exportBusy.set(true);
    this.exportError.set(null);
    req.subscribe({
      next: (blob) => {
        saveBlob(blob, filename);
        this.exportBusy.set(false);
      },
      error: async (e) => {
        this.exportError.set(await toApiError(e));
        this.exportBusy.set(false);
      },
    });
  }

  // ----- CAD files and variations -----
  protected download(v: Variation, kind: '3dm' | 'stl'): void {
    this.api.downloadLink(v.id, kind).subscribe({
      next: (l) => window.location.assign(l.url),
      error: async (e) =>
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 }),
    });
  }

  protected editDesign(): void {
    const d = this.design.value();
    if (!d) return;
    const data: NewDesignData = {
      categories: this.categories(),
      tags: this.tagPool(),
      design: {
        id: d.id,
        code: d.code,
        name: d.name,
        categoryId: d.category_id,
        tagIds: d.tags.map((t) => t.id),
      },
    };
    this.dialog
      .open<NewDesignDialogComponent, NewDesignData, string>(NewDesignDialogComponent, {
        data,
        width: '460px',
      })
      .afterClosed()
      .subscribe((code) => {
        if (!code) return;
        this.snack.open('Design saved', 'OK', { duration: 3000 });
        this.refresh();
      });
  }

  protected deleteDesign(): void {
    const d = this.design.value();
    if (!d) return;
    const n = d.variations.length;
    const warning =
      `Delete ${d.code} and its ${n} variation${n === 1 ? '' : 's'}? ` +
      'The design disappears from the catalog. Its code stays reserved and cannot be reused.';
    if (!confirm(warning)) return;
    this.api.deleteDesign(d.id).subscribe({
      next: () => {
        this.snack.open(`Deleted ${d.code}`, 'OK', { duration: 4000 });
        void this.router.navigate(['/']);
      },
      error: async (e) =>
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 }),
    });
  }

  protected addVariation(): void {
    const d = this.design.value();
    if (!d) return;
    const data: NewVariationData = { designId: d.id, designCode: d.code, metals: this.metals() };
    this.dialog
      .open<NewVariationDialogComponent, NewVariationData, number>(NewVariationDialogComponent, {
        data,
        width: '560px',
      })
      .afterClosed()
      .subscribe((id) => {
        if (id) this.router.navigate(['/designs', d.code, 'variations', id, 'edit']);
      });
  }

  protected refresh(): void {
    this.reloadTick.update((n) => n + 1);
  }

  protected size(bytes: number | null): string {
    if (bytes === null) return '';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }
}
