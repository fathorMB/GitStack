import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import {
  Button,
  Checkbox,
  ConfirmDialog,
  CopyButton,
  DataTable,
  EmptyState,
  FormField,
  PasswordInput,
  StatusBadge,
  TextInput,
  ToastProvider,
  useToast,
} from '.';

describe('Button', () => {
  it('applica la variante e non e\' submit di default', () => {
    render(<Button variant="danger">Delete</Button>);
    const b = screen.getByRole('button', { name: 'Delete' });
    expect(b).toHaveClass('btn', 'btn-danger');
    expect(b).toHaveAttribute('type', 'button');
  });
});

describe('FormField', () => {
  it('collega etichetta, hint ed errore al campo', () => {
    render(
      <FormField label="Name" hint="Shown in lists." error="Name is required.">
        <TextInput />
      </FormField>,
    );
    const input = screen.getByLabelText('Name');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Shown in lists. Name is required.');
    expect(screen.getByRole('alert')).toHaveTextContent('Name is required.');
  });

  it('senza errore non e\' invalid', () => {
    render(
      <FormField label="Name">
        <TextInput />
      </FormField>,
    );
    expect(screen.getByLabelText('Name')).not.toHaveAttribute('aria-invalid');
  });
});

describe('PasswordInput', () => {
  it('mostra e nasconde con il pulsante, da tastiera', async () => {
    const user = userEvent.setup();
    render(
      <FormField label="Password">
        <PasswordInput />
      </FormField>,
    );
    const input = screen.getByLabelText('Password');
    expect(input).toHaveAttribute('type', 'password');
    await user.tab(); // campo
    await user.tab(); // pulsante
    await user.keyboard('{Enter}');
    expect(input).toHaveAttribute('type', 'text');
    expect(screen.getByRole('button', { name: 'Hide password' })).toHaveAttribute('aria-pressed', 'true');
  });
});

describe('Checkbox', () => {
  it('si attiva con la barra spaziatrice ed e\' etichettato', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Checkbox label="Init README" onCheckedChange={onChange} />);
    await user.tab();
    expect(screen.getByRole('checkbox', { name: 'Init README' })).toHaveFocus();
    await user.keyboard(' ');
    expect(onChange).toHaveBeenCalledWith(true);
  });
});

describe('DataTable', () => {
  const cols = [{ key: 'n', header: 'Name', render: (r: { n: string }) => r.n }];
  it('rende intestazioni e righe', () => {
    render(<DataTable caption="Things" columns={cols} rows={[{ n: 'a' }, { n: 'b' }]} rowKey={(r) => r.n} />);
    expect(screen.getByRole('table', { name: 'Things' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getAllByRole('row')).toHaveLength(3);
  });
  it('mostra lo stato vuoto senza righe', () => {
    render(
      <DataTable caption="Things" columns={cols} rows={[]} rowKey={(r: { n: string }) => r.n} empty={<EmptyState title="Nothing" />} />,
    );
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.getByText('Nothing')).toBeInTheDocument();
  });
});

describe('StatusBadge', () => {
  it('ha testo e icona (non solo colore)', () => {
    const { container } = render(<StatusBadge status="open" />);
    expect(screen.getByText('Open')).toBeInTheDocument();
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
  });
});

describe('ConfirmDialog', () => {
  it('conferma, chiude con Esc e ha titolo e descrizione accessibili', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Delete?"
        description="Cannot be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={onConfirm}
      />,
    );
    const dialog = screen.getByRole('dialog', { name: 'Delete?' });
    expect(dialog).toHaveAccessibleDescription('Cannot be undone.');
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onConfirm).toHaveBeenCalledOnce();
    await user.keyboard('{Escape}');
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe('Toast', () => {
  function Trigger() {
    const { toast } = useToast();
    return <Button onClick={() => toast('Saved', 'success')}>go</Button>;
  }
  it('mostra il messaggio', async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <Trigger />
      </ToastProvider>,
    );
    await user.click(screen.getByRole('button', { name: 'go' }));
    expect(await screen.findByText('Saved')).toBeInTheDocument();
  });
});

describe('CopyButton', () => {
  it('copia negli appunti e lo annuncia', async () => {
    const user = userEvent.setup(); // installa un clipboard finto
    render(<CopyButton value="abc" />);
    await user.click(screen.getByRole('button', { name: /copy/i }));
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Copied to clipboard'));
    expect(await navigator.clipboard.readText()).toBe('abc');
  });
});
