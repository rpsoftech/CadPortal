import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import {
  Subject,
  catchError,
  debounceTime,
  from,
  map,
  merge,
  of,
  startWith,
  switchMap,
  tap,
} from 'rxjs';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { CatalogSelection } from '../../core/catalog-selection';
import { gramsToCarats } from '../../core/stone-units';
import { saveBlob } from '../../shared/download';
import {
  CadZipKind,
  DesignFilter,
  DesignImportResult,
  DesignListItem,
  DesignSort,
  TAG_GROUPS,
} from '../../core/models';
import { NewDesignData, NewDesignDialogComponent } from '../../shared/new-design-dialog.component';
import { SelectionBarComponent } from '../../shared/selection-bar.component';
import { CostSelectedData, CostSelectedDialogComponent } from './cost-selected-dialog.component';
import { DownloadCadData, DownloadCadDialogComponent } from './download-cad-dialog.component';
import { PrintJobBagsData, PrintJobBagsDialogComponent } from './print-job-bags-dialog.component';
import { DesignImportDialogComponent } from './design-import-dialog.component';
import { SetupChecklistComponent } from '../../shared/setup-checklist.component';
import { percent, positiveDecimal } from '../../shared/validators';
import { EMPTY_FILTERS, filtersToParams, paramsToFilters } from './catalog-url';

interface ListState {
  loading: boolean;
  items: DesignListItem[];
  /** All matches before paging. */
  total: number;
  error: string;
  /** The metal the request was made for; weights on the cards belong to this metal. */
  metal: string | null;
}

/** "3.050 – 6.100 g", or a single value when min equals max. */
export function weightRange(min: string | null, max: string | null): string | null {
  if (min === null || max === null) return null;
  return min === max ? `${fixed(min, 3)} g` : `${fixed(min, 3)} – ${fixed(max, 3)} g`;
}

@Component({
  selector: 'app-catalog',
  imports: [
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatCardModule,
    MatCheckboxModule,
    MatChipsModule,
    MatFormFieldModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    SetupChecklistComponent,
    SelectionBarComponent,
  ],
  templateUrl: './catalog.component.html',
  styleUrl: './catalog.component.scss',
})
export class CatalogComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  /** The filters of the address the screen was opened with (a shared link, or back from a design). */
  private readonly initial = paramsToFilters(this.route.snapshot.queryParams);
  protected readonly auth = inject(AuthService);
  protected readonly selection = inject(CatalogSelection);

  protected readonly categories = toSignal(this.api.categories().pipe(catchError(() => of([]))), {
    initialValue: [],
  });
  protected readonly tags = toSignal(this.api.tags().pipe(catchError(() => of([]))), {
    initialValue: [],
  });
  protected readonly metals = toSignal(
    this.api.metals().pipe(
      map((ms) => ms.filter((m) => m.active)),
      catchError(() => of([])),
    ),
    { initialValue: [] },
  );

  protected readonly stoneCodes = toSignal(
    this.api.stoneCodes().pipe(
      map((cs) => cs.filter((c) => c.active)),
      catchError(() => of([])),
    ),
    { initialValue: [] },
  );
  protected readonly baselineCode = computed(
    () => this.stoneCodes().find((c) => c.is_default_baseline)?.code ?? null,
  );

  protected readonly sortOptions: { value: DesignSort; label: string; needsMetal: boolean }[] = [
    { value: 'code', label: 'Design code', needsMetal: false },
    { value: 'net_asc', label: 'Net weight, light first', needsMetal: true },
    { value: 'net_desc', label: 'Net weight, heavy first', needsMetal: true },
    { value: 'gross_asc', label: 'Gross weight, light first', needsMetal: true },
    { value: 'gross_desc', label: 'Gross weight, heavy first', needsMetal: true },
    { value: 'stone_pct_asc', label: 'Stone %, low first', needsMetal: true },
    { value: 'stone_pct_desc', label: 'Stone %, high first', needsMetal: true },
  ];

  protected readonly tagGroups = computed(() =>
    TAG_GROUPS.map((name) => ({
      name,
      tags: this.tags().filter((t) => t.tag_group === name),
    })).filter((g) => g.tags.length > 0),
  );

  protected readonly form = new FormGroup({
    q: new FormControl(this.initial.value.q),
    categoryId: new FormControl<number | null>(this.initial.value.categoryId),
    metalCode: new FormControl<string | null>(this.initial.value.metalCode),
    stoneCode: new FormControl<string | null>(this.initial.value.stoneCode),
    minNetWt: new FormControl(this.initial.value.minNetWt, positiveDecimal(3)),
    maxNetWt: new FormControl(this.initial.value.maxNetWt, positiveDecimal(3)),
    minGrossWt: new FormControl(this.initial.value.minGrossWt, positiveDecimal(3)),
    maxGrossWt: new FormControl(this.initial.value.maxGrossWt, positiveDecimal(3)),
    minStonePct: new FormControl(this.initial.value.minStonePct, percent(2)),
    maxStonePct: new FormControl(this.initial.value.maxStonePct, percent(2)),
    maxStoneCts: new FormControl(this.initial.value.maxStoneCts, positiveDecimal(8)),
    sort: new FormControl<DesignSort>(this.initial.value.sort, { nonNullable: true }),
    tagIds: new FormControl<number[]>(this.initial.value.tagIds, { nonNullable: true }),
  });

  /** The weight and stone % filters and the weight sorts only mean something for a metal. */
  protected readonly hasMetal = toSignal(
    this.form.controls.metalCode.valueChanges.pipe(
      startWith(this.form.controls.metalCode.value),
      map((m) => m !== null),
    ),
    { initialValue: this.form.controls.metalCode.value !== null },
  );

  /** The filter panel is folded away on a phone; on wider screens it is always shown. */
  protected readonly filtersOpen = signal(false);

  /** How many filters are set (the search box is not counted: it is always in view). */
  protected readonly activeFilters = toSignal(
    this.form.valueChanges.pipe(
      startWith(null),
      map(() => {
        const v = this.form.getRawValue();
        const set = (x: string | null) => (x ?? '').trim() !== '';
        return (
          (v.categoryId !== null ? 1 : 0) +
          (v.metalCode !== null ? 1 : 0) +
          (v.stoneCode !== null ? 1 : 0) +
          (v.sort !== 'code' ? 1 : 0) +
          [
            v.minNetWt,
            v.maxNetWt,
            v.minGrossWt,
            v.maxGrossWt,
            v.minStonePct,
            v.maxStonePct,
            v.maxStoneCts,
          ].filter(set).length +
          v.tagIds.length
        );
      }),
    ),
    { initialValue: 0 },
  );

  protected readonly pageSizeOptions = [12, 24, 48, 96];
  protected readonly pageIndex = signal(this.initial.pageIndex);
  protected readonly pageSize = signal(this.initial.pageSize);
  private readonly pageChanged$ = new Subject<void>();

  protected readonly state = toSignal(
    merge(
      // A changed filter or search starts again from the first page.
      this.form.valueChanges.pipe(
        debounceTime(300),
        tap(() => this.pageIndex.set(0)),
      ),
      this.pageChanged$,
    ).pipe(
      startWith(null),
      switchMap(() => {
        const size = this.pageSize();
        const f = { ...this.filter(), limit: size, offset: this.pageIndex() * size };
        this.syncUrl();
        const metal = f.metalCode ?? null;
        return this.api.listDesigns(f).pipe(
          tap(({ items }) => {
            // The last page can vanish (e.g. after deletions elsewhere): go back to the first.
            if (items.length === 0 && this.pageIndex() > 0) {
              this.pageIndex.set(0);
              this.pageChanged$.next();
            }
          }),
          map(({ items, total }): ListState => ({
            loading: false,
            items,
            total,
            error: '',
            metal,
          })),
          startWith<ListState>({ loading: true, items: [], total: 0, error: '', metal }),
          catchError((e) =>
            from(toApiError(e)).pipe(
              map((err): ListState => ({
                loading: false,
                items: [],
                total: 0,
                error: err.message,
                metal,
              })),
            ),
          ),
        );
      }),
    ),
    { initialValue: { loading: true, items: [], total: 0, error: '', metal: null } as ListState },
  );

  /** Every design on the page is ticked (a partly ticked page counts as not all). */
  protected readonly pageAllSelected = computed(() => {
    const items = this.state().items;
    return items.length > 0 && items.every((d) => this.selection.has(d.id));
  });
  protected readonly pageSomeSelected = computed(
    () => !this.pageAllSelected() && this.state().items.some((d) => this.selection.has(d.id)),
  );

  protected togglePage(checked: boolean): void {
    const items = this.state().items;
    if (checked) this.selection.addMany(items);
    else this.selection.removeMany(items.map((d) => d.id));
  }

  protected costSelected(): void {
    const data: CostSelectedData = { designs: this.selection.items() };
    this.dialog
      .open<CostSelectedDialogComponent, CostSelectedData, number>(CostSelectedDialogComponent, {
        data,
        width: '860px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((sheetId) => {
        if (!sheetId) return;
        this.snack
          .open('Saved as a costing sheet', 'Open', { duration: 8000 })
          .onAction()
          .subscribe(() => this.router.navigate(['/costing-sheets', sheetId]));
      });
  }

  /** Opens the download dialog for the STL or 3dm files of the selected designs. */
  protected downloadCad(kind: CadZipKind): void {
    const data: DownloadCadData = { kind, designs: this.selection.items() };
    this.dialog.open<DownloadCadDialogComponent, DownloadCadData>(DownloadCadDialogComponent, {
      data,
      width: '520px',
      maxWidth: '95vw',
    });
  }

  /** Opens the dialog that prints the job bags of the selected designs into one PDF. */
  protected printJobBags(): void {
    const data: PrintJobBagsData = { designs: this.selection.items() };
    this.dialog.open<PrintJobBagsDialogComponent, PrintJobBagsData>(PrintJobBagsDialogComponent, {
      data,
      width: '520px',
      maxWidth: '95vw',
    });
  }

  protected onPage(e: PageEvent): void {
    this.pageIndex.set(e.pageIndex);
    this.pageSize.set(e.pageSize);
    this.pageChanged$.next();
  }

  /**
   * Keeps the address in step with the filters, so a link shares them and coming back from a design
   * restores them. It replaces the history entry instead of adding one per keystroke.
   */
  private syncUrl(): void {
    const params = filtersToParams(
      this.form.getRawValue(),
      this.pageIndex(),
      this.pageSize(),
    );
    void this.router.navigate([], { queryParams: params, replaceUrl: true });
  }

  /** Invalid number fields are left out of the request instead of sent as garbage. */
  private filter(): DesignFilter {
    const c = this.form.controls;
    const num = (ctl: FormControl<string | null>) => (ctl.valid ? ctl.value : null);
    const metal = c.metalCode.value;
    return {
      q: c.q.value,
      categoryId: c.categoryId.value,
      metalCode: metal,
      stoneCode: c.stoneCode.value,
      // stone % and the weight sorts are about the chosen metal: without one they are not sent
      minStonePct: metal ? num(c.minStonePct) : null,
      maxStonePct: metal ? num(c.maxStonePct) : null,
      sort: metal && c.sort.value !== 'code' ? c.sort.value : null,
      tagIds: c.tagIds.value,
      minNetWt: num(c.minNetWt),
      maxNetWt: num(c.maxNetWt),
      minGrossWt: num(c.minGrossWt),
      maxGrossWt: num(c.maxGrossWt),
      // typed in grams like every other stone weight; the API filters in carats (1 ct = 0.2 g)
      maxStoneCts: gramsToCarats(num(c.maxStoneCts)),
    };
  }

  protected net(d: DesignListItem): string | null {
    return weightRange(d.net_min, d.net_max);
  }
  protected gross(d: DesignListItem): string | null {
    return weightRange(d.gross_min, d.gross_max);
  }

  protected reset(): void {
    this.form.reset({ ...EMPTY_FILTERS });
  }

  /** "Stone 0.069 g · 2.23 %" for the shown variation, or null when there is no stone figure. */
  protected stone(d: DesignListItem): string | null {
    if (d.stone_status !== 'ok' || d.stone_g == null || d.stone_pct == null) return null;
    return `${fixed(d.stone_g, 3)} g · ${fixed(d.stone_pct, 2)} %`;
  }

  protected newDesign(): void {
    const data: NewDesignData = { categories: this.categories(), tags: this.tags() };
    this.dialog
      .open<NewDesignDialogComponent, NewDesignData, string>(NewDesignDialogComponent, { data })
      .afterClosed()
      .subscribe((code) => {
        if (code) this.router.navigate(['/designs', code]);
      });
  }

  protected importDesigns(): void {
    this.dialog
      .open<DesignImportDialogComponent, void, DesignImportResult>(DesignImportDialogComponent, {
        width: '720px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((res) => {
        if (!res?.designs) return;
        const shown = res.codes.slice(0, 3).join(', ');
        const more = res.codes.length > 3 ? ` and ${res.codes.length - 3} more` : '';
        this.snack.open(
          `Imported ${res.designs} design${res.designs === 1 ? '' : 's'} (${shown}${more})`,
          'OK',
          { duration: 6000 },
        );
        this.pageIndex.set(0);
        this.pageChanged$.next();
      });
  }

  protected readonly exporting = signal(false);

  protected exportDesigns(): void {
    this.exporting.set(true);
    this.api.designsCsv().subscribe({
      next: (blob) => {
        saveBlob(blob, 'designs.csv');
        this.exporting.set(false);
      },
      error: async (e) => {
        this.exporting.set(false);
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 });
      },
    });
  }
}
