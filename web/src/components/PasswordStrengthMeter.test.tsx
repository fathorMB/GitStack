import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PasswordStrengthMeter } from './PasswordStrengthMeter';

describe('PasswordStrengthMeter', () => {
  it('non mostra niente con il campo vuoto', () => {
    const { container } = render(<PasswordStrengthMeter password="" />);
    expect(container).toBeEmptyDOMElement();
  });

  it('espone il punteggio come meter accessibile', () => {
    render(<PasswordStrengthMeter password="Cavallo Batteria Graffetta 42!" />);
    const meter = screen.getByRole('meter', { name: 'Password strength' });
    expect(meter).toHaveAttribute('aria-valuenow', '4');
    expect(screen.getByText('Strong')).toBeInTheDocument();
  });

  it('segnala una password comune', () => {
    render(<PasswordStrengthMeter password="password1234" />);
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '0');
    expect(screen.getByText(/very common password/)).toBeInTheDocument();
  });
});
