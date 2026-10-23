import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { ResetLink, User } from '../../core/models';

/** The full address of a reset link: the portal's own address plus the path the server returned. */
export function fullLink(origin: string, link: ResetLink): string {
  return origin.replace(/\/$/, '') + link.path;
}

/**
 * An admin makes a one-hour reset link for someone who forgot their password and hands it over (chat,
 * phone, in person). The person chooses their own password, so the admin never knows it. The link is
 * shown once: it is not stored anywhere readable, and making another one replaces it.
 */
@Component({
  selector: 'app-reset-link-dialog',
  imports: [DatePipe, MatButtonModule, MatDialogModule, MatProgressBarModule],
  template: `
    <h2 mat-dialog-title>Reset link for {{ user.email }}</h2>
    <mat-dialog-content class="col">
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      } @else if (!link()) {
        <mat-progress-bar aria-label="Loading" mode="indeterminate" />
      } @else {
        <p>
          Send this link to {{ user.name || user.email }}. It works once, until
          {{ link()!.expires_at | date: 'shortTime' }}, and they choose the new password
          themselves. Their current sessions end when they do.
        </p>
        <textarea
          class="link mono"
          readonly
          rows="4"
          aria-label="Reset link"
          (focus)="$any($event.target).select()"
          >{{ address() }}</textarea
        >
        <p class="muted small">
          Anyone who has this link can set the password, so give it only to {{ user.email }}. It
          cannot be shown again; make a new one if it gets lost, which also cancels this one.
        </p>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      @if (link()) {
        <button matButton type="button" (click)="copy()">
          {{ copied() ? 'Copied' : 'Copy link' }}
        </button>
      }
      <button matButton="filled" type="button" mat-dialog-close>Done</button>
    </mat-dialog-actions>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      min-width: min(420px, 80vw);
    }
    .link {
      width: 100%;
      box-sizing: border-box;
      resize: none;
      padding: 8px;
      border-radius: 8px;
      border: 1px solid var(--mat-sys-outline);
      background: var(--mat-sys-surface-container);
      color: var(--mat-sys-on-surface);
      word-break: break-all;
    }
  `,
})
export class ResetLinkDialogComponent {
  protected readonly user = inject<User>(MAT_DIALOG_DATA);
  private readonly api = inject(ApiService);

  protected readonly link = signal<ResetLink | null>(null);
  protected readonly error = signal('');
  protected readonly copied = signal(false);

  constructor() {
    this.api.userResetLink(this.user.id).subscribe({
      next: (l) => this.link.set(l),
      error: async (e) => this.error.set((await toApiError(e)).message),
    });
  }

  protected address(): string {
    const l = this.link();
    return l ? fullLink(window.location.origin, l) : '';
  }

  protected copy(): void {
    void navigator.clipboard
      .writeText(this.address())
      .then(() => this.copied.set(true))
      .catch(() => this.copied.set(false)); // the text is selectable, so it can still be copied by hand
  }
}
