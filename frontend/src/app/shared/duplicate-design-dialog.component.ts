import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { ApiService } from '../core/api.service';
import { toApiError } from '../core/errors';

export interface DuplicateDesignData {
  designId: number;
  code: string;
  name: string;
  variations: { id: number; code: string }[];
  tagCount: number;
}

/** The suggested name of a copy: "<name> (copy)". */
export function copyName(name: string): string {
  const n = name.trim();
  return n ? `${n} (copy)` : '';
}

/**
 * Makes a new design from an existing one: same category, the next automatic code, optionally the same
 * tags, and the chosen variations with their base input and stone seats (weights are recomputed).
 * CAD files, photos and videos are never copied.
 */
@Component({
  selector: 'app-duplicate-design-dialog',
  imports: [
    ReactiveFormsModule,
    MatButtonModule,
    MatCheckboxModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
  ],
  template: `
    <h2 mat-dialog-title>Duplicate {{ data.code }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" maxlength="200" />
          <mat-hint>The new design gets the next code in its category automatically.</mat-hint>
        </mat-form-field>

        @if (data.variations.length) {
          <fieldset>
            <legend>Variations to copy</legend>
            @for (v of data.variations; track v.id) {
              <div>
                <mat-checkbox
                  [checked]="picked().has(v.id)"
                  (change)="toggle(v.id, $event.checked)"
                  >{{ v.code }}</mat-checkbox
                >
              </div>
            }
          </fieldset>
        } @else {
          <p class="muted">This design has no variations; only the design itself is copied.</p>
        }

        <mat-checkbox formControlName="copyTags"
          >Copy the tags ({{ data.tagCount }})</mat-checkbox
        >

        <p class="note">
          Each copied variation keeps its base input and stone seats; weights are worked out again.
          <strong>CAD files and photos are not copied</strong>: a CAD file can be stored only once,
          so upload them to the new design yourself.
        </p>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="busy()">Duplicate</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
      min-width: 340px;
    }
    fieldset {
      border: 1px solid var(--mat-sys-outline-variant, #ccc);
      border-radius: 8px;
      padding: 8px 12px;
      margin: 0;
    }
    .note {
      margin: 0;
    }
  `,
})
export class DuplicateDesignDialogComponent {
  protected readonly data = inject<DuplicateDesignData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<DuplicateDesignDialogComponent, string>);
  private readonly api = inject(ApiService);

  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly picked = signal(new Set(this.data.variations.map((v) => v.id)));
  protected readonly form = new FormGroup({
    name: new FormControl(copyName(this.data.name), { nonNullable: true }),
    copyTags: new FormControl(true, { nonNullable: true }),
  });

  protected toggle(id: number, on: boolean): void {
    this.picked.update((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  save(): void {
    const v = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    this.api
      .duplicateDesign(this.data.designId, {
        name: v.name.trim() || undefined,
        // In source order; an empty list copies no variation (omitting it would copy all).
        variation_ids: this.data.variations.filter((x) => this.picked().has(x.id)).map((x) => x.id),
        copy_tags: v.copyTags,
      })
      .subscribe({
        next: (d) => this.ref.close(d.code),
        error: async (e) => {
          this.busy.set(false);
          this.error.set((await toApiError(e)).message);
        },
      });
  }
}
