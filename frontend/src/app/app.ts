import { Component, effect, inject, untracked } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterLink, RouterOutlet } from '@angular/router';
import { AuthService } from './core/auth.service';
import { TokenDialogComponent } from './shared/token-dialog.component';

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, RouterLink, MatToolbarModule, MatButtonModule],
  template: `
    <mat-toolbar>
      <a routerLink="/" class="brand">CadPortal</a>
      <span class="spacer"></span>
      <span class="muted role">{{ auth.role() ?? 'not signed in' }}</span>
      <button matButton="outlined" (click)="openToken()">
        {{ auth.token() ? 'Change token' : 'Sign in' }}
      </button>
    </mat-toolbar>
    <router-outlet />
  `,
  styles: `
    .brand {
      color: inherit;
      text-decoration: none;
      font-weight: 600;
      font-size: 1.2rem;
    }
    .spacer {
      flex: 1;
    }
    .role {
      margin-right: 12px;
    }
  `,
})
export class App {
  protected readonly auth = inject(AuthService);
  private readonly dialog = inject(MatDialog);
  private open = false;

  constructor() {
    // The API answered 401: ask for a token once (not again while the dialog is open).
    effect(() => {
      if (this.auth.promptRequested() > 0) untracked(() => this.openToken());
    });
  }

  openToken(): void {
    if (this.open) return;
    this.open = true;
    this.dialog
      .open(TokenDialogComponent, { width: '520px' })
      .afterClosed()
      .subscribe(() => (this.open = false));
  }
}
