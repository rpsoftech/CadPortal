import { Component, computed, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { Router, RouterLink } from '@angular/router';
import { catchError, debounceTime, from, map, of, startWith, switchMap } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { DesignFilter, DesignListItem, TAG_GROUPS } from '../../core/models';
import { NewDesignData, NewDesignDialogComponent } from '../../shared/new-design-dialog.component';
import { positiveDecimal } from '../../shared/validators';

interface ListState {
  loading: boolean;
  items: DesignListItem[];
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
    MatProgressBarModule,
    MatSelectModule,
  ],
  templateUrl: './catalog.component.html',
  styleUrl: './catalog.component.scss',
})
export class CatalogComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly router = inject(Router);

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
    categoryId: new FormControl<number | null>(null),
    metalCode: new FormControl<string | null>(null),
    minNetWt: new FormControl('', positiveDecimal(3)),
    maxNetWt: new FormControl('', positiveDecimal(3)),
    minGrossWt: new FormControl('', positiveDecimal(3)),
    maxGrossWt: new FormControl('', positiveDecimal(3)),
    maxStoneCts: new FormControl('', positiveDecimal(4)),
    tagIds: new FormControl<number[]>([], { nonNullable: true }),
  });

  protected readonly state = toSignal(
    this.form.valueChanges.pipe(
      startWith(null),
      debounceTime(300),
      switchMap(() => {
        const f = this.filter();
        const metal = f.metalCode ?? null;
        return this.api.listDesigns(f).pipe(
          map((items): ListState => ({ loading: false, items, error: '', metal })),
          startWith<ListState>({ loading: true, items: [], error: '', metal }),
          catchError((e) =>
            from(toApiError(e)).pipe(
              map((err): ListState => ({ loading: false, items: [], error: err.message, metal })),
            ),
          ),
        );
      }),
    ),
    { initialValue: { loading: true, items: [], error: '', metal: null } as ListState },
  );

  /** Invalid number fields are left out of the request instead of sent as garbage. */
  private filter(): DesignFilter {
    const c = this.form.controls;
    const num = (ctl: FormControl<string | null>) => (ctl.valid ? ctl.value : null);
    return {
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
}
