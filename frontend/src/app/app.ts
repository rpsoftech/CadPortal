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
      <a routerLink="/" class="brand" aria-label="Golden Oak Jewels LLP, home">
        <img src="brand/mark-cream.png" alt="" class="brand-mark" />
        <span>Golden Oak</span>
      </a>
      @if (auth.user(); as u) {
        <nav class="links" aria-label="Main">
          @if (auth.canEdit()) {
            <a matButton routerLink="/costing-sheets">Costing sheets</a>
            <a matButton routerLink="/removed">Removed</a>
          }
          @if (auth.isAdmin()) {
            <a matButton routerLink="/master-data">Master data</a>
            <a matButton routerLink="/users">Users</a>
            <a matButton routerLink="/audit">Audit</a>
            <a matButton routerLink="/backups">Backups</a>
          }
        </nav>
        @if (auth.canEdit()) {
          <!-- On a phone the links do not fit beside the account button, so they live in a menu. -->
          <button matButton class="nav-menu" [matMenuTriggerFor]="nav" aria-label="Menu">Menu</button>
          <mat-menu #nav="matMenu">
            <a mat-menu-item routerLink="/costing-sheets">Costing sheets</a>
            <a mat-menu-item routerLink="/removed">Removed</a>
            @if (auth.isAdmin()) {
              <a mat-menu-item routerLink="/master-data">Master data</a>
              <a mat-menu-item routerLink="/users">Users</a>
              <a mat-menu-item routerLink="/audit">Audit</a>
              <a mat-menu-item routerLink="/backups">Backups</a>
            }
          </mat-menu>
        }
        <span class="spacer"></span>
        <button
          matButton="outlined"
          class="account"
          [matMenuTriggerFor]="menu"
          aria-label="Account menu"
        >
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
      display: inline-flex;
      align-items: center;
      gap: 10px;
      letter-spacing: 0.12em;
      text-transform: uppercase;
      color: inherit;
      text-decoration: none;
      font-weight: 600;
      font-size: 1.2rem;
      margin-right: 12px;
    }
    .brand-mark {
      height: 36px;
      width: auto;
    }
    .spacer {
      flex: 1;
    }
    .links {
      display: flex;
    }
    .nav-menu {
      display: none;
    }
    .account {
      max-width: 55vw;
    }
    .account ::ng-deep .mdc-button__label {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    @media (max-width: 760px) {
      .links {
        display: none;
      }
      .nav-menu {
        display: inline-flex;
      }
      .brand {
        font-size: 1.05rem;
        margin-right: 0;
      }
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
