import { Component, inject } from '@angular/core';
import { FormControl, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

export interface OrderStatusDialogData {
  title: string;
  /** What will happen, in a sentence. */
  message: string;
  confirmLabel: string;
  /** A destructive step (cancel): the button says so and the dialog is not confirmed by Enter alone. */
  danger?: boolean;
}

/** Asks for confirmation and an optional note; closes with the note (possibly empty) or nothing. */
@Component({
  selector: 'app-order-status-dialog',
  imports: [ReactiveFormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule],
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content>
      <p>{{ data.message }}</p>
      <mat-form-field appearance="outline" class="full">
        <mat-label>Note (optional, kept in the history)</mat-label>
        <textarea matInput [formControl]="note" rows="3" maxlength="500"></textarea>
      </mat-form-field>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Keep as is</button>
      <button matButton="filled" [mat-dialog-close]="note.value.trim()" cdkFocusInitial>
        {{ data.confirmLabel }}
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    .full {
      width: 100%;
    }
  `,
})
export class OrderStatusDialogComponent {
  protected readonly data = inject<OrderStatusDialogData>(MAT_DIALOG_DATA);
  protected readonly note = new FormControl('', { nonNullable: true, validators: Validators.maxLength(500) });
}
