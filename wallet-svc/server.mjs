import express from 'express';
import { Wallet, CoinType } from '@iota/sdk';

const app = express();
app.use(express.json());

const PORT = process.env.PORT || 8090;
// Basic in‑memory registry (replace with DB + KMS/Stronghold in production)
const wallets = new Map();

// Create a wallet for a given user.  Returns the wallet identifier so that
// future operations can be tied to the same wallet instance.
app.post('/wallets', async (req, res) => {
  try {
    const userId = req.body.userId;
    if (!userId) return res.status(400).json({ error: 'userId required' });
    if (wallets.has(userId)) return res.json({ ok: true, walletId: userId });

    // NOTE: configure proper clientOptions + Stronghold for production
    const wallet = new Wallet({
      storagePath: `wallet-${userId}`,
      clientOptions: {
        // TODO: point to your IOTA node; using public endpoints is rate‑limited
        nodes: ['https://api.mainnet.iota.org']
      },
      coinType: CoinType.IOTA
    });
    wallets.set(userId, wallet);
    res.json({ ok: true, walletId: userId });
  } catch (e) {
    console.error(e);
    res.status(500).json({ error: e.message });
  }
});

// Retrieve or create an address for a given wallet.  This should be backed
// by stronghold or other secure key storage in production.
app.get('/wallets/:id/address', async (req, res) => {
  try {
    const wallet = wallets.get(req.params.id);
    if (!wallet) return res.status(404).json({ error: 'not found' });
    const account = await wallet.createAccount({ alias: req.params.id });
    const addr = (await account.addresses())[0] || await account.generateAddress();
    res.json({ address: addr.address });
  } catch (e) {
    console.error(e);
    res.status(500).json({ error: e.message });
  }
});

// Very simplified "charge" endpoint.  Sends tokens from the wallet to a
// destination address.  This implementation ignores confirmation and
// settlement details and should not be used as‑is in production.
app.post('/wallets/:id/charge', async (req, res) => {
  try {
    const { amount, to } = req.body;
    const wallet = wallets.get(req.params.id);
    if (!wallet) return res.status(404).json({ error: 'not found' });
    const account = await wallet.getAccount(req.params.id);
    const tx = await account.send(amount, to);
    res.json({ tx });
  } catch (e) {
    console.error(e);
    res.status(500).json({ error: e.message });
  }
});

app.listen(PORT, () => {
  console.log('wallet-svc listening on', PORT);
});