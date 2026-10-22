import { Component, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { RouterLink } from '@angular/router';
import { catchError, of } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';

/**
 * "Forgot password". With a mail server set up it asks for the address and says the same thing whether or
 * not the address has an account (the server does not tell, and neither does this page). Without one it
 * says so and sends people to an admin, who can make a one-hour reset link on the Users screen.
 */
@Component({
  selector: 'app-forgot-password',
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
          <mat-card-title>Forgot your password?</mat-card-title>
        </mat-card-header>
        @if (config.isLoading()) {
          <mat-progress-bar mode="indeterminate" />
        } @else if (!emailEnabled()) {
          <mat-card-content>
            <p>
              Resetting by email is not set up here. Ask an administrator for a reset link: they
              can make one for you in a few seconds, and you choose the new password yourself.
            </p>
          </mat-card-content>
          <mat-card-actions align="end">
            <a matButton routerLink="/login">Back to sign in</a>
          </mat-card-actions>
        } @else if (sent()) {
          <mat-card-content>
            <p role="status">
              If that address belongs to an account, a link to choose a new password is on its way.
              It works once and for one hour. Nothing arrived after a few minutes? Check the spam
              folder, or ask an administrator.
            </p>
          </mat-card-content>
          <mat-card-actions align="end">
            <a matButton routerLink="/login">Back to sign in</a>
          </mat-card-actions>
        } @else {
          <form [formGroup]="form" (ngSubmit)="submit()">
            <mat-card-content>
              <p class="muted">
                Enter the email address you sign in with and we will send you a link to choose a
                new password.
              </p>
              <mat-form-field appearance="outline">
                <mat-label>Email</mat-label>
                <input matInput type="email" formControlName="email" autocomplete="username" />
                @if (form.controls.email.invalid && form.controls.email.touched) {
                  <mat-error>Enter your email address</mat-error>
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
              <a matButton routerLink="/login">Back to sign in</a>
              <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">
                Send the link
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
    }
  `,
})
export class ForgotPasswordComponent {
  private readonly api = inject(ApiService);

  protected readonly config = rxResource({
    stream: () => this.api.authConfig().pipe(catchError(() => of(null))),
  });
  protected readonly busy = signal(false);
  protected readonly sent = signal(false);
  protected readonly error = signal('');

  protected readonly form = new FormGroup({
    email: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.email],
    }),
  });

  protected emailEnabled(): boolean {
    return this.config.value()?.password_reset_email === true;
  }

  protected submit(): void {
    if (this.form.invalid || this.busy()) return;
    this.busy.set(true);
    this.error.set('');
    this.api.forgotPassword(this.form.getRawValue().email.trim()).subscribe({
      next: () => {
        this.busy.set(false);
        this.sent.set(true);
      },
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
