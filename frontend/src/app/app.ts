import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatMenuModule } from '@angular/material/menu';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterLink, RouterOutlet } from '@angular/router';
import { AuthService } from './core/auth.service';
import { ChangePasswordDialogComponent } from './shared/change-password-dialog.component';

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, RouterLink, MatToolbarModule, MatButtonModule, MatMenuModule],
  template: `
    <mat-toolbar>
      <a routerLink="/" class="brand">CadPortal</a>
      @if (auth.user(); as u) {
        @if (auth.isAdmin()) {
          <a matButton routerLink="/users">Users</a>
        }
        <span class="spacer"></span>
        <button matButton="outlined" [matMenuTriggerFor]="menu" aria-label="Account menu">
          {{ u.name || u.email }} · {{ u.role }}
        </button>
        <mat-menu #menu="matMenu">
          <div class="menu-id muted">{{ u.email }}</div>
          <button mat-menu-item (click)="changePassword()">Change password</button>
          <button mat-menu-item (click)="auth.logout()">Sign out</button>
        </mat-menu>
      }
    </mat-toolbar>
    <router-outlet />
  `,
  styles: `
    .brand {
      color: inherit;
      text-decoration: none;
      font-weight: 600;
      font-size: 1.2rem;
      margin-right: 12px;
    }
    .spacer {
      flex: 1;
    }
    .menu-id {
      padding: 8px 16px;
      font-size: 0.85rem;
    }
  `,
})
export class App {
  protected readonly auth = inject(AuthService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);

  protected changePassword(): void {
    this.dialog
      .open<ChangePasswordDialogComponent, void, boolean>(ChangePasswordDialogComponent, {
        width: '420px',
      })
      .afterClosed()
      .subscribe((ok) => {
        if (ok) this.snack.open('Password changed', 'OK', { duration: 3000 });
      });
  }
}
