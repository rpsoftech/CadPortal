import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { ApiService } from '../core/api.service';
import { toApiError } from '../core/errors';
import { BaseInput, Metal } from '../core/models';
import { BaseInputComponent } from './base-input.component';

export interface NewVariationData {
  designId: number;
  designCode: string;
  metals: Metal[];
}

/** Creates the next CODE-Vn variation; closes with the new variation id. */
@Component({
  selector: 'app-new-variation-dialog',
  imports: [MatDialogModule, MatButtonModule, BaseInputComponent],
  template: `
    <h2 mat-dialog-title>New variation of {{ data.designCode }}</h2>
    <mat-dialog-content>
      <p class="muted">
        Choose how the weight is derived. Seats and CAD files are added on the next screen.
      </p>
      <app-base-input [metals]="data.metals" (valueChange)="base.set($event)" />
      @if (error()) {
        <div class="error-box">{{ error() }}</div>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" (click)="save()" [disabled]="!base() || busy()">Create</button>
    </mat-dialog-actions>
  `,
})
export class NewVariationDialogComponent {
  protected readonly data = inject<NewVariationData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<NewVariationDialogComponent, number>);
  private readonly api = inject(ApiService);

  protected readonly base = signal<BaseInput | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  save(): void {
    const base = this.base();
    if (!base) return;
    this.busy.set(true);
    this.error.set('');
    this.api.createVariation(this.data.designId, base).subscribe({
      next: (v) => this.ref.close(v.id),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
