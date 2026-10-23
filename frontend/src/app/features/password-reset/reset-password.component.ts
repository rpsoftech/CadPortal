import { Component, inject, input, signal } from '@angular/core';
import {
  AbstractControl,
  FormControl,
  FormGroup,
  ReactiveFormsModule,
  ValidationErrors,
  Validators,
} from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router, RouterLink } from '@angular/router';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { toApiError } from '../../core/errors';
import { PASSWORD_MAX, PASSWORD_MIN } from '../../shared/validators';

function matches(group: AbstractControl): ValidationErrors | null {
  return group.get('next')?.value === group.get('confirm')?.value ? null : { mismatch: true };
}

/**
 * The page a reset link opens: choose a new password. The token comes from the ?token= address and is
 * sent to the server as it is; it is never shown or stored. A used or expired link is explained, with a
 * way to ask for another.
 */
@Component({
  selector: 'app-reset-password',
  imports: [
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatCardModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
  ],
  template: `
    <div class="wrap">
      <mat-card appearance="outlined">
        <mat-card-header>
          <h1 mat-card-title>Choose a new password</h1>
        </mat-card-header>
        @if (!token()) {
          <mat-card-content>
            <p>
              This page needs the link from your email, or the one an administrator gave you. Open
              that link again.
            </p>
          </mat-card-content>
          <mat-card-actions align="end">
            <a matButton routerLink="/forgot-password">Ask for a new link</a>
          </mat-card-actions>
        } @else if (expired()) {
          <mat-card-content>
            <p role="alert">
              This link has already been used or has expired. Links work once, for one hour.
            </p>
          </mat-card-content>
          <mat-card-actions align="end">
            <a matButton="filled" routerLink="/forgot-password">Ask for a new link</a>
          </mat-card-actions>
        } @else {
          <form [formGroup]="form" (ngSubmit)="submit()">
            <mat-card-content>
              <mat-form-field appearance="outline">
                <mat-label>New password</mat-label>
                <input
                  matInput
                  type="password"
                  formControlName="next"
                  autocomplete="new-password"
                />
                <mat-hint>At least {{ min }} characters</mat-hint>
                @if (form.controls.next.invalid && form.controls.next.touched) {
                  <mat-error>Use {{ min }} to {{ max }} characters</mat-error>
                }
              </mat-form-field>
              <mat-form-field appearance="outline">
                <mat-label>Repeat the new password</mat-label>
                <input
                  matInput
                  type="password"
                  formControlName="confirm"
                  autocomplete="new-password"
                />
                @if (form.errors?.['mismatch'] && form.controls.confirm.touched) {
                  <mat-error>The passwords do not match</mat-error>
                }
              </mat-form-field>
              @if (error()) {
                <div class="error-box" role="alert">{{ error() }}</div>
              }
              @if (busy()) {
                <mat-progress-bar aria-label="Loading" mode="indeterminate" />
              }
            </mat-card-content>
            <mat-card-actions align="end">
              <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">
                Save the password
              </button>
            </mat-card-actions>
          </form>
        }
      </mat-card>
    </div>
  `,
  styles: `
    .wrap {
      display: flex;
      justify-content: center;
      padding: 48px 16px;
    }
    mat-card {
      width: 100%;
      max-width: 400px;
    }
    mat-card-content {
      display: flex;
      flex-direction: column;
      padding-top: 16px;
      gap: 4px;
    }
  `,
})
export class ResetPasswordComponent {
  /** Bound from the ?token= query parameter by withComponentInputBinding(). */
  readonly token = input<string>();

  private readonly api = inject(ApiService);
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly snack = inject(MatSnackBar);

  protected readonly min = PASSWORD_MIN;
  protected readonly max = PASSWORD_MAX;
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly expired = signal(false);

  protected readonly form = new FormGroup(
    {
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

  protected submit(): void {
    const token = this.token();
    if (!token || this.form.invalid || this.busy()) return;
    this.busy.set(true);
    this.error.set('');
    this.api.resetPasswordWithToken(token, this.form.getRawValue().next).subscribe({
      next: () => {
        // Anyone signed in on this browser is signed out: the old session no longer works anyway.
        this.snack.open('Password changed. Sign in with the new one.', 'OK', { duration: 6000 });
        if (this.auth.isAuthenticated()) this.auth.logout();
        else void this.router.navigate(['/login']);
      },
      error: async (e) => {
        const message = (await toApiError(e)).message;
        this.busy.set(false);
        // A weak password can be corrected on the same link; a dead link cannot.
        if (message.toLowerCase().includes('reset link')) this.expired.set(true);
        else this.error.set(message);
      },
    });
  }
}
