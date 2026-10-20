import { Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { User, UserRole } from '../../core/models';
import { PASSWORD_MAX, PASSWORD_MIN } from '../../shared/validators';

export const ROLES: { value: UserRole; label: string; hint: string }[] = [
  { value: 'admin', label: 'Admin', hint: 'Everything, including master data and users' },
  { value: 'designer', label: 'Designer', hint: 'Designs, seats, CAD uploads, costing' },
  {
    value: 'production',
    label: 'Production',
    hint: 'Read-only, job bags and CAD downloads; no rates',
  },
];

/** Creates a user, or edits name, role and active state of an existing one. */
@Component({
  selector: 'app-user-dialog',
  imports: [
    ReactiveFormsModule,
    MatDialogModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
  ],
  template: `
    <h2 mat-dialog-title>{{ data ? 'Edit ' + data.email : 'New user' }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        @if (!data) {
          <mat-form-field appearance="outline">
            <mat-label>Email</mat-label>
            <input matInput type="email" formControlName="email" autocomplete="off" />
            @if (form.controls.email.invalid && form.controls.email.touched) {
              <mat-error>Enter a valid email address</mat-error>
            }
          </mat-form-field>
        }
        <mat-form-field appearance="outline">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" maxlength="100" />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label>Role</mat-label>
          <mat-select formControlName="role">
            @for (r of roles; track r.value) {
              <mat-option [value]="r.value">{{ r.label }}: {{ r.hint }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        @if (!data) {
          <mat-form-field appearance="outline">
            <mat-label>Initial password</mat-label>
            <input
              matInput
              type="password"
              formControlName="password"
              autocomplete="new-password"
            />
            <mat-hint
              >At least {{ min }} characters. Share it securely; they can change it.</mat-hint
            >
            @if (form.controls.password.invalid && form.controls.password.touched) {
              <mat-error>Use {{ min }} to {{ max }} characters</mat-error>
            }
          </mat-form-field>
        } @else {
          <mat-slide-toggle formControlName="active">Active (can sign in)</mat-slide-toggle>
        }
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">
          {{ data ? 'Save' : 'Create' }}
        </button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 4px;
      min-width: 360px;
    }
  `,
})
export class UserDialogComponent {
  protected readonly data = inject<User | null>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<UserDialogComponent, User>);
  private readonly api = inject(ApiService);

  protected readonly roles = ROLES;
  protected readonly min = PASSWORD_MIN;
  protected readonly max = PASSWORD_MAX;
  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected readonly form = new FormGroup({
    email: new FormControl('', {
      nonNullable: true,
      validators: this.data ? [] : [Validators.required, Validators.email],
    }),
    name: new FormControl(this.data?.name ?? '', { nonNullable: true }),
    role: new FormControl<UserRole>(this.data?.role ?? 'designer', { nonNullable: true }),
    password: new FormControl('', {
      nonNullable: true,
      validators: this.data
        ? []
        : [
            Validators.required,
            Validators.minLength(PASSWORD_MIN),
            Validators.maxLength(PASSWORD_MAX),
          ],
    }),
    active: new FormControl(this.data?.active ?? true, { nonNullable: true }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    const v = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    const req = this.data
      ? this.api.updateUser(this.data.id, { name: v.name.trim(), role: v.role, active: v.active })
      : this.api.createUser({
          email: v.email.trim(),
          name: v.name.trim(),
          password: v.password,
          role: v.role,
        });
    req.subscribe({
      next: (u) => this.ref.close(u),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}

/** An admin sets a new password for someone; their existing sessions end. */
@Component({
  selector: 'app-reset-password-dialog',
  imports: [
    ReactiveFormsModule,
    MatDialogModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
  ],
  template: `
    <h2 mat-dialog-title>Reset password for {{ user.email }}</h2>
    <form [formGroup]="form" (ngSubmit)="save()">
      <mat-dialog-content class="col">
        <p class="muted">Their current sessions are ended immediately.</p>
        <mat-form-field appearance="outline">
          <mat-label>New password</mat-label>
          <input matInput type="password" formControlName="password" autocomplete="new-password" />
          <mat-hint>At least {{ min }} characters</mat-hint>
        </mat-form-field>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>Cancel</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Reset</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      min-width: 340px;
    }
  `,
})
export class ResetPasswordDialogComponent {
  protected readonly user = inject<User>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<ResetPasswordDialogComponent, boolean>);
  private readonly api = inject(ApiService);

  protected readonly min = PASSWORD_MIN;
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly form = new FormGroup({
    password: new FormControl('', {
      nonNullable: true,
      validators: [
        Validators.required,
        Validators.minLength(PASSWORD_MIN),
        Validators.maxLength(PASSWORD_MAX),
      ],
    }),
  });

  protected save(): void {
    if (this.form.invalid) return;
    this.busy.set(true);
    this.error.set('');
    this.api.resetUserPassword(this.user.id, this.form.getRawValue().password).subscribe({
      next: () => this.ref.close(true),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
