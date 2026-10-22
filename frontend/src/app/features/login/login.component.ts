import { Component, inject, input, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router, RouterLink } from '@angular/router';
import { AuthService } from '../../core/auth.service';
import { toApiError } from '../../core/errors';
import { safeReturnUrl } from '../../core/guards';

@Component({
  selector: 'app-login',
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
          <mat-card-title>Sign in to CadPortal</mat-card-title>
        </mat-card-header>
        <form [formGroup]="form" (ngSubmit)="submit()">
          <mat-card-content>
            <mat-form-field appearance="outline">
              <mat-label>Email</mat-label>
              <input
                matInput
                type="email"
                formControlName="email"
                autocomplete="username"
                autofocus
              />
              @if (form.controls.email.invalid && form.controls.email.touched) {
                <mat-error>Enter your email address</mat-error>
              }
            </mat-form-field>
            <mat-form-field appearance="outline">
              <mat-label>Password</mat-label>
              <input
                matInput
                type="password"
                formControlName="password"
                autocomplete="current-password"
              />
              @if (form.controls.password.invalid && form.controls.password.touched) {
                <mat-error>Enter your password</mat-error>
              }
            </mat-form-field>
            @if (error()) {
              <div class="error-box" role="alert">{{ error() }}</div>
            }
            @if (busy()) {
              <mat-progress-bar mode="indeterminate" />
            }
          </mat-card-content>
          <mat-card-actions align="end">
            <a matButton routerLink="/forgot-password">Forgot password?</a>
            <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">
              Sign in
            </button>
          </mat-card-actions>
        </form>
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
    }
  `,
})
export class LoginComponent {
  /** Bound from the ?returnUrl= query parameter by withComponentInputBinding(). */
  readonly returnUrl = input<string>();

  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);

  protected readonly busy = signal(false);
  protected readonly error = signal('');

  protected readonly form = new FormGroup({
    email: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.email],
    }),
    password: new FormControl('', { nonNullable: true, validators: [Validators.required] }),
  });

  protected submit(): void {
    if (this.form.invalid || this.busy()) return;
    const { email, password } = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    this.auth.login(email.trim(), password).subscribe({
      next: () => void this.router.navigateByUrl(safeReturnUrl(this.returnUrl())),
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.form.controls.password.reset('');
        this.busy.set(false);
      },
    });
  }
}
