/**
 * Universal Server Template JavaScript - SPEC v1.0 Compliant
 * NO alert(), confirm(), or prompt() - Professional UI only
 */

// ===== UI Class =====
class UI {
  /**
   * Show a modal dialog
   * @param {Object} options - Modal configuration
   * @returns {Promise} Resolves with user action
   */
  static async showModal(options) {
    const { title, message, confirmText = 'OK', cancelText = 'Cancel', type = 'info' } = options;

    return new Promise((resolve) => {
      const modal = document.createElement('div');
      modal.className = 'modal active';
      modal.innerHTML = `
        <div class="modal-backdrop"></div>
        <div class="modal-content">
          <div class="modal-header">
            <h2 class="modal-title">${title}</h2>
            <button class="modal-close" aria-label="Close">×</button>
          </div>
          <div class="modal-body">${message}</div>
          <div class="modal-footer">
            ${cancelText ? `<button class="btn btn-secondary" data-action="cancel">${cancelText}</button>` : ''}
            <button class="btn btn-primary" data-action="confirm">${confirmText}</button>
          </div>
        </div>
      `;

      document.getElementById('modal-container').appendChild(modal);

      // Event handlers
      const handleClose = (confirmed) => {
        modal.remove();
        resolve(confirmed);
      };

      modal.querySelector('.modal-close').addEventListener('click', () => handleClose(false));
      modal.querySelector('.modal-backdrop').addEventListener('click', () => handleClose(false));
      modal.querySelector('[data-action="confirm"]').addEventListener('click', () => handleClose(true));

      const cancelBtn = modal.querySelector('[data-action="cancel"]');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', () => handleClose(false));
      }

      // ESC key to close
      const escHandler = (e) => {
        if (e.key === 'Escape') {
          document.removeEventListener('keydown', escHandler);
          handleClose(false);
        }
      };
      document.addEventListener('keydown', escHandler);
    });
  }

  /**
   * Show a toast notification
   * @param {String} message - Notification message
   * @param {String} type - Type: success, error, warning, info
   * @param {Number} duration - Duration in ms (0 = permanent)
   */
  static showToast(message, type = 'info', duration = 5000) {
    const toast = document.createElement('div');
    const icons = {
      success: '✅',
      error: '❌',
      warning: '⚠️',
      info: 'ℹ️'
    };

    toast.className = `alert alert-${type}`;
    toast.innerHTML = `
      <div class="alert-icon">${icons[type] || icons.info}</div>
      <div class="alert-content">
        <div class="alert-message">${message}</div>
      </div>
      <button class="alert-close" aria-label="Close">×</button>
    `;

    document.getElementById('toast-container').appendChild(toast);

    // Close handler
    toast.querySelector('.alert-close').addEventListener('click', () => {
      toast.remove();
    });

    // Auto-dismiss
    if (duration > 0) {
      setTimeout(() => {
        toast.remove();
      }, duration);
    }
  }

  /**
   * Show confirmation dialog
   * @param {Object} options - Confirmation options
   * @returns {Promise<Boolean>}
   */
  static async confirm(options) {
    const {
      title = 'Confirm Action',
      message = 'Are you sure?',
      confirmText = 'Confirm',
      cancelText = 'Cancel',
      confirmClass = 'btn-primary'
    } = options;

    return new Promise((resolve) => {
      const modal = document.createElement('div');
      modal.className = 'modal active';
      modal.innerHTML = `
        <div class="modal-backdrop"></div>
        <div class="modal-content">
          <div class="modal-header">
            <h2 class="modal-title">${title}</h2>
            <button class="modal-close" aria-label="Close">×</button>
          </div>
          <div class="modal-body">${message}</div>
          <div class="modal-footer">
            <button class="btn btn-secondary" data-action="cancel">${cancelText}</button>
            <button class="btn ${confirmClass}" data-action="confirm">${confirmText}</button>
          </div>
        </div>
      `;

      document.getElementById('modal-container').appendChild(modal);

      const handleClose = (confirmed) => {
        modal.remove();
        resolve(confirmed);
      };

      modal.querySelector('.modal-close').addEventListener('click', () => handleClose(false));
      modal.querySelector('.modal-backdrop').addEventListener('click', () => handleClose(false));
      modal.querySelector('[data-action="cancel"]').addEventListener('click', () => handleClose(false));
      modal.querySelector('[data-action="confirm"]').addEventListener('click', () => handleClose(true));

      // ESC key
      const escHandler = (e) => {
        if (e.key === 'Escape') {
          document.removeEventListener('keydown', escHandler);
          handleClose(false);
        }
      };
      document.addEventListener('keydown', escHandler);
    });
  }

  /**
   * Convert timestamps to user's local timezone
   */
  static convertTimestamps() {
    document.querySelectorAll('time[data-unix]').forEach(el => {
      const unix = parseInt(el.dataset.unix);
      const date = new Date(unix * 1000);
      const userTz = Intl.DateTimeFormat().resolvedOptions().timeZone;

      // Format based on user preferences
      const options = {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        timeZoneName: 'short'
      };

      el.textContent = date.toLocaleString('en-US', options);
      el.title = `Original: ${el.dataset.original || el.textContent}`;
    });
  }

  /**
   * Update relative times (e.g., "2 minutes ago")
   */
  static updateRelativeTimes() {
    document.querySelectorAll('[data-format="relative"]').forEach(el => {
      const unix = parseInt(el.dataset.unix);
      const date = new Date(unix * 1000);
      const now = new Date();
      const diff = now - date;

      const seconds = Math.floor(diff / 1000);
      const minutes = Math.floor(seconds / 60);
      const hours = Math.floor(minutes / 60);
      const days = Math.floor(hours / 24);

      let relative;
      if (seconds < 60) {
        relative = 'just now';
      } else if (minutes < 60) {
        relative = `${minutes} minute${minutes !== 1 ? 's' : ''} ago`;
      } else if (hours < 24) {
        relative = `${hours} hour${hours !== 1 ? 's' : ''} ago`;
      } else {
        relative = `${days} day${days !== 1 ? 's' : ''} ago`;
      }

      el.textContent = relative;
    });
  }
}

// ===== Mobile Menu Toggle =====
document.addEventListener('DOMContentLoaded', () => {
  const menuToggle = document.querySelector('.mobile-menu-toggle');
  const mainNav = document.getElementById('main-nav');

  if (menuToggle && mainNav) {
    menuToggle.addEventListener('click', () => {
      mainNav.classList.toggle('active');
    });
  }

  // Initialize timezone conversion
  UI.convertTimestamps();

  // Update relative times every minute
  setInterval(UI.updateRelativeTimes, 60000);
  UI.updateRelativeTimes();
});

// ===== Banner Dismiss =====
document.addEventListener('click', (e) => {
  if (e.target.classList.contains('banner-dismiss')) {
    e.target.closest('.banner').remove();
  }
});

// ===== Form Validation Helpers =====
class FormValidator {
  static validateUsername(username) {
    const regex = /^[a-zA-Z0-9_]{3,50}$/;
    return regex.test(username);
  }

  static validateEmail(email) {
    const regex = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
    return regex.test(email);
  }

  static validatePassword(password, isAdmin = false) {
    const minLength = isAdmin ? 12 : 8;
    if (password.length < minLength) return false;

    const hasLower = /[a-z]/.test(password);
    const hasUpper = /[A-Z]/.test(password);
    const hasNumber = /[0-9]/.test(password);

    return hasLower && hasUpper && hasNumber;
  }

  static getPasswordStrength(password) {
    let strength = 0;

    if (password.length >= 8) strength++;
    if (password.length >= 12) strength++;
    if (/[a-z]/.test(password) && /[A-Z]/.test(password)) strength++;
    if (/[0-9]/.test(password)) strength++;
    if (/[^a-zA-Z0-9]/.test(password)) strength++;

    if (strength <= 2) return { level: 'weak', label: 'Weak', className: 'strength-weak' };
    if (strength <= 3) return { level: 'fair', label: 'Fair', className: 'strength-fair' };
    if (strength <= 4) return { level: 'good', label: 'Good', className: 'strength-good' };
    return { level: 'strong', label: 'Strong', className: 'strength-strong' };
  }
}

// ===== API Helper =====
class API {
  static async request(url, options = {}) {
    const defaultOptions = {
      headers: {
        'Content-Type': 'application/json'
      }
    };

    const mergedOptions = { ...defaultOptions, ...options };

    try {
      const response = await fetch(url, mergedOptions);
      const data = await response.json();

      if (!response.ok) {
        throw new Error(data.error?.message || 'Request failed');
      }

      return data;
    } catch (error) {
      console.error('API Error:', error);
      throw error;
    }
  }

  static async get(url) {
    return this.request(url);
  }

  static async post(url, body) {
    return this.request(url, {
      method: 'POST',
      body: JSON.stringify(body)
    });
  }

  static async put(url, body) {
    return this.request(url, {
      method: 'PUT',
      body: JSON.stringify(body)
    });
  }

  static async delete(url) {
    return this.request(url, {
      method: 'DELETE'
    });
  }
}

// ===== Loading State Helper =====
class LoadingState {
  static set(element, loading = true) {
    if (loading) {
      element.disabled = true;
      element.classList.add('loading');
      element.dataset.originalText = element.textContent;
      element.textContent = 'Loading...';
    } else {
      element.disabled = false;
      element.classList.remove('loading');
      element.textContent = element.dataset.originalText || element.textContent.replace('Loading...', '');
    }
  }
}

// ===== Token Display (Show Once) =====
function displayTokenOnce(token) {
  UI.showModal({
    title: 'API Token Created',
    message: `
      <div class="token-display">
        <p><strong>This token will only be shown once!</strong></p>
        <p>Please copy it now and store it securely.</p>
        <div class="token-value">
          <code>${token}</code>
          <button class="btn btn-sm btn-secondary" onclick="navigator.clipboard.writeText('${token}'); UI.showToast('Token copied to clipboard!', 'success')">Copy</button>
        </div>
        <p class="text-muted">You will not be able to retrieve this token again.</p>
      </div>
    `,
    confirmText: 'I have saved this token',
    cancelText: ''
  });
}

// ===== Session Management =====
class SessionManager {
  static async terminateSession(sessionId) {
    const confirmed = await UI.confirm({
      title: 'Terminate Session',
      message: 'Are you sure you want to end this session? You will be logged out on that device.',
      confirmText: 'Terminate',
      confirmClass: 'btn-danger'
    });

    if (confirmed) {
      try {
        await API.delete(`/api/v1/user/sessions/${sessionId}`);
        UI.showToast('Session terminated successfully', 'success');
        // Reload to update session list
        setTimeout(() => location.reload(), 1000);
      } catch (error) {
        UI.showToast('Failed to terminate session', 'error');
      }
    }
  }

  static async terminateAllSessions() {
    const confirmed = await UI.confirm({
      title: 'Terminate All Sessions',
      message: 'This will log you out from all devices except the current one. Are you sure?',
      confirmText: 'Terminate All',
      confirmClass: 'btn-danger'
    });

    if (confirmed) {
      try {
        await API.post('/api/v1/user/sessions/terminate-all');
        UI.showToast('All other sessions terminated', 'success');
        setTimeout(() => location.reload(), 1000);
      } catch (error) {
        UI.showToast('Failed to terminate sessions', 'error');
      }
    }
  }
}

// ===== Theme Toggle =====
function toggleTheme() {
  const currentTheme = document.body.dataset.theme || 'dark';
  const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
  document.body.dataset.theme = newTheme;
  localStorage.setItem('theme', newTheme);
}

// Load saved theme
document.addEventListener('DOMContentLoaded', () => {
  const savedTheme = localStorage.getItem('theme') || 'dark';
  document.body.dataset.theme = savedTheme;
});

// ===== Delete Confirmation Helper =====
async function confirmDelete(itemName, deleteUrl) {
  const confirmed = await UI.confirm({
    title: `Delete ${itemName}`,
    message: `Are you sure you want to delete this ${itemName}? This action cannot be undone.`,
    confirmText: 'Delete',
    confirmClass: 'btn-danger'
  });

  if (confirmed) {
    try {
      await API.delete(deleteUrl);
      UI.showToast(`${itemName} deleted successfully`, 'success');
      setTimeout(() => location.reload(), 1000);
    } catch (error) {
      UI.showToast(`Failed to delete ${itemName}`, 'error');
    }
  }
}

// Export for use in other scripts
window.UI = UI;
window.API = API;
window.FormValidator = FormValidator;
window.LoadingState = LoadingState;
window.SessionManager = SessionManager;
