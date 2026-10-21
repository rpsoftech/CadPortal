import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router, RouterLink } from '@angular/router';
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
import { saveBlob } from '../../shared/download';
import { DesignFilter, DesignImportResult, DesignListItem, TAG_GROUPS } from '../../core/models';
import { NewDesignData, NewDesignDialogComponent } from '../../shared/new-design-dialog.component';
import { DesignImportDialogComponent } from './design-import-dialog.component';
import { SetupChecklistComponent } from '../../shared/setup-checklist.component';
import { positiveDecimal } from '../../shared/validators';

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
    MatChipsModule,
    MatFormFieldModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    SetupChecklistComponent,
  ],
  templateUrl: './catalog.component.html',
  styleUrl: './catalog.component.scss',
})
export class CatalogComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  private readonly router = inject(Router);
  protected readonly auth = inject(AuthService);

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

  protected readonly tagGroups = computed(() =>
    TAG_GROUPS.map((name) => ({
      name,
      tags: this.tags().filter((t) => t.tag_group === name),
    })).filter((g) => g.tags.length > 0),
  );

  protected readonly form = new FormGroup({
    q: new FormControl(''),
    categoryId: new FormControl<number | null>(null),
    metalCode: new FormControl<string | null>(null),
    minNetWt: new FormControl('', positiveDecimal(3)),
    maxNetWt: new FormControl('', positiveDecimal(3)),
    minGrossWt: new FormControl('', positiveDecimal(3)),
    maxGrossWt: new FormControl('', positiveDecimal(3)),
    maxStoneCts: new FormControl('', positiveDecimal(4)),
    tagIds: new FormControl<number[]>([], { nonNullable: true }),
  });

  protected readonly pageSizeOptions = [12, 24, 48, 96];
  protected readonly pageIndex = signal(0);
  protected readonly pageSize = signal(24);
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

  protected onPage(e: PageEvent): void {
    this.pageIndex.set(e.pageIndex);
    this.pageSize.set(e.pageSize);
    this.pageChanged$.next();
  }

  /** Invalid number fields are left out of the request instead of sent as garbage. */
  private filter(): DesignFilter {
    const c = this.form.controls;
    const num = (ctl: FormControl<string | null>) => (ctl.valid ? ctl.value : null);
    return {
      q: c.q.value,
      categoryId: c.categoryId.value,
      metalCode: c.metalCode.value,
      tagIds: c.tagIds.value,
      minNetWt: num(c.minNetWt),
      maxNetWt: num(c.maxNetWt),
      minGrossWt: num(c.minGrossWt),
      maxGrossWt: num(c.maxGrossWt),
      maxStoneCts: num(c.maxStoneCts),
    };
  }

  protected net(d: DesignListItem): string | null {
    return weightRange(d.net_min, d.net_max);
  }
  protected gross(d: DesignListItem): string | null {
    return weightRange(d.gross_min, d.gross_max);
  }

  protected reset(): void {
    this.form.reset({ tagIds: [] });
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
