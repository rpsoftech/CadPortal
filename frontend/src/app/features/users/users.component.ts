import { DatePipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { User } from '../../core/models';
import { ResetPasswordDialogComponent, UserDialogComponent } from './user-dialogs.component';

@Component({
  selector: 'app-users',
  imports: [DatePipe, MatButtonModule, MatProgressBarModule],
  template: `
    <div class="page">
      <div class="row head">
        <h1>Users</h1>
        <span class="spacer"></span>
        <button matButton="filled" (click)="create()">New user</button>
      </div>

      @if (users.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      @if (users.error()) {
        <div class="error-box" role="alert">Could not load users.</div>
      }

      <div class="scroll">
        <table class="plain">
          <thead>
            <tr>
              <th>Email</th>
              <th>Name</th>
              <th>Role</th>
              <th>Status</th>
              <th>Last sign-in</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (u of users.value() ?? []; track u.id) {
              <tr [class.disabled]="!u.active">
                <td>
                  {{ u.email }}
                  @if (u.id === auth.user()?.id) {
                    <span class="muted">(you)</span>
                  }
                </td>
                <td>{{ u.name || '–' }}</td>
                <td>{{ u.role }}</td>
                <td>{{ u.active ? 'Active' : 'Disabled' }}</td>
                <td>{{ u.last_login_at ? (u.last_login_at | date: 'medium') : 'Never' }}</td>
                <td class="actions">
                  <button matButton (click)="edit(u)">Edit</button>
                  <button matButton (click)="resetPassword(u)">Reset password</button>
                </td>
              </tr>
            }
          </tbody>
        </table>
      </div>
    </div>
  `,
  styles: `
    .head h1 {
      margin: 0;
    }
    .spacer {
      flex: 1;
    }
    .scroll {
      overflow-x: auto;
    }
    tr.disabled td {
      color: var(--mat-sys-on-surface-variant);
    }
    .actions {
      white-space: nowrap;
    }
  `,
})
export class UsersComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  protected readonly auth = inject(AuthService);

  private readonly tick = signal(0);
  protected readonly users = rxResource({
    params: () => this.tick(),
    stream: () => this.api.users(),
  });

  private refresh(): void {
    this.tick.update((n) => n + 1);
  }

  protected create(): void {
    this.dialog
      .open<UserDialogComponent, User | null, User>(UserDialogComponent, { data: null })
      .afterClosed()
      .subscribe((u) => {
        if (!u) return;
        this.snack.open(`Created ${u.email}`, 'OK', { duration: 3000 });
        this.refresh();
      });
  }

  protected edit(user: User): void {
    this.dialog
      .open<UserDialogComponent, User | null, User>(UserDialogComponent, { data: user })
      .afterClosed()
      .subscribe((u) => {
        if (!u) return;
        this.snack.open(`Saved ${u.email}`, 'OK', { duration: 3000 });
        this.refresh();
      });
  }

  protected resetPassword(user: User): void {
    this.dialog
      .open<ResetPasswordDialogComponent, User, boolean>(ResetPasswordDialogComponent, {
        data: user,
      })
      .afterClosed()
      .subscribe((ok) => {
        if (ok) this.snack.open(`Password reset for ${user.email}`, 'OK', { duration: 4000 });
      });
  }
}
