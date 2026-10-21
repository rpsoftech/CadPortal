import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { ApiService } from '../core/api.service';
import { toApiError } from '../core/errors';
import { Category, TAG_GROUPS, Tag } from '../core/models';

/** The code after the last number handed out in a category, for example LR0007. */
export function nextDesignCode(prefix: string, lastNumber: number): string {
  return `${prefix}${String(lastNumber + 1).padStart(4, '0')}`;
}

export interface NewDesignData {
  categories: Category[];
  tags: Tag[];
  /** When set, the dialog edits this design instead of creating one. The code is fixed. */
  design?: { id: number; code: string; name: string; categoryId: number; tagIds: number[] };
}

@Component({
  selector: 'app-new-design-dialog',
  imports: [
    ReactiveFormsModule,
    MatDialogModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
  ],
  template: `
    <h2 mat-dialog-title>{{ data.design ? 'Edit ' + data.design.code : 'New master design' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Category</mat-label>
          <mat-select formControlName="categoryId">
            @for (c of data.categories; track c.id) {
              <mat-option [value]="c.id" [disabled]="!data.design && !c.code_prefix">
                {{ c.name }}{{ data.design || c.code_prefix ? '' : ' (no code prefix yet)' }}
              </mat-option>
            }
          </mat-select>
          <mat-error>Choose a category</mat-error>
        </mat-form-field>
        @if (!data.design) {
          <p class="code-note" aria-live="polite">
            @if (nextCode(); as next) {
              Design code: <strong class="mono">{{ next }}</strong> (assigned automatically)
            } @else {
              Choose a category: the design code is assigned automatically from its prefix.
            }
          </p>
        }
        <mat-form-field appearance="outline">
          <mat-label>Tags</mat-label>
          <mat-select formControlName="tagIds" multiple>
            @for (g of groups; track g.name) {
              @if (g.tags.length) {
                <mat-optgroup [label]="g.name">
                  @for (t of g.tags; track t.id) {
                    <mat-option [value]="t.id">{{ t.name }}</mat-option>
                  }
                </mat-optgroup>
              }
            }
          </mat-select>
        </mat-form-field>
        @if (error()) {
          <div class="error-box">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button mat-button type="button" mat-dialog-close>Cancel</button>
        <button mat-flat-button type="submit" [disabled]="form.invalid || busy()">
          {{ data.design ? 'Save' : 'Create' }}
        </button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 4px;
      min-width: 340px;
    }
    .code-note {
      margin: 0 0 8px;
    }
  `,
})
export class NewDesignDialogComponent {
  protected readonly data = inject<NewDesignData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<NewDesignDialogComponent, string>);
  private readonly api = inject(ApiService);

  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly groups = TAG_GROUPS.map((name) => ({
    name,
    tags: this.data.tags.filter((t) => t.tag_group === name),
  }));

  protected readonly form = new FormGroup({
    name: new FormControl(this.data.design?.name ?? '', { nonNullable: true }),
    categoryId: new FormControl<number | null>(
      this.data.design?.categoryId ?? null,
      Validators.required,
    ),
    tagIds: new FormControl<number[]>(this.data.design?.tagIds ?? [], { nonNullable: true }),
  });

  private readonly selectedCategory = toSignal(this.form.controls.categoryId.valueChanges, {
    initialValue: this.form.controls.categoryId.value,
  });
  /** The code the next design in the chosen category will get (the server decides for real). */
  protected readonly nextCode = computed(() => {
    const c = this.data.categories.find((x) => x.id === this.selectedCategory());
    return c?.code_prefix ? nextDesignCode(c.code_prefix, c.next_seq) : null;
  });

  save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    const design = this.data.design;
    const body = { name: v.name.trim(), category_id: v.categoryId!, tag_ids: v.tagIds };
    const req = design ? this.api.updateDesign(design.id, body) : this.api.createDesign(body);
    req.subscribe({
      next: (d) => this.ref.close(d.code),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
