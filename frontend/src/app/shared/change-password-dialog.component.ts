import { Component, inject, signal } from '@angular/core';
import {
  AbstractControl,
  FormControl,
  FormGroup,
  ReactiveFormsModule,
  ValidationErrors,
  Validators,
} from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AuthService } from '../core/auth.service';
import { toApiError } from '../core/errors';
import { PASSWORD_MAX, PASSWORD_MIN } from './validators';

function matches(group: AbstractControl): ValidationErrors | null {
  return group.get('next')?.value === group.get('confirm')?.value ? null : { mismatch: true };
}

@Component({
  selector: 'app-change-password-dialog',
  imports: [
    ReactiveFormsModule,
    MatDialogModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
  ],
  template: `
    <h2 mat-dialog-title>Change password</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <mat-form-field appearance="outline">
          <mat-label>Current password</mat-label>
          <input
            matInput
            type="password"
            formControlName="current"
            autocomplete="current-password"
          />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>New password</mat-label>
          <input matInput type="password" formControlName="next" autocomplete="new-password" />
          <mat-hint>At least {{ min }} characters</mat-hint>
          @if (form.controls.next.invalid && form.controls.next.touched) {
            <mat-error>Use {{ min }} to {{ max }} characters</mat-error>
          }
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Repeat new password</mat-label>
          <input matInput type="password" formControlName="confirm" autocomplete="new-password" />
          @if (form.errors?.['mismatch'] && form.controls.confirm.touched) {
            <mat-error>The passwords do not match</mat-error>
          }
        </mat-form-field>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Change</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 4px;
      min-width: 320px;
    }
  `,
})
export class ChangePasswordDialogComponent {
  private readonly auth = inject(AuthService);
  private readonly ref = inject(MatDialogRef<ChangePasswordDialogComponent, boolean>);

  protected readonly min = PASSWORD_MIN;
  protected readonly max = PASSWORD_MAX;
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected readonly form = new FormGroup(
    {
      current: new FormControl('', { nonNullable: true, validators: [Validators.required] }),
      next: new FormControl('', {
        nonNullable: true,
        validators: [
          Validators.required,
          Validators.minLength(PASSWORD_MIN),
          Validators.maxLength(PASSWORD_MAX),
        ],
      }),
      confirm: new FormControl('', { nonNullable: true, validators: [Validators.required] }),
    },
    { validators: matches },
  );

  protected save(): void {
    if (this.form.invalid) return;
    const { current, next } = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    this.auth.changePassword(current, next).subscribe({
      next: () => this.ref.close(true),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
