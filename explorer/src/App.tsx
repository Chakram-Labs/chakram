import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Home from './pages/Home'
import Blocks from './pages/Blocks'
import BlockDetail from './pages/BlockDetail'
import Txs from './pages/Txs'
import TxDetail from './pages/TxDetail'
import AddressDetail from './pages/AddressDetail'
import Tokens from './pages/Tokens'
import TokenDetail from './pages/TokenDetail'
import Validators from './pages/Validators'
import VerifyContract from './pages/VerifyContract'

export default function App() {
  return (
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/blocks" element={<Blocks />} />
          <Route path="/block/:height" element={<BlockDetail />} />
          <Route path="/txs" element={<Txs />} />
          <Route path="/tx/:hash" element={<TxDetail />} />
          <Route path="/address/:address" element={<AddressDetail />} />
          <Route path="/tokens" element={<Tokens />} />
          <Route path="/token/:address" element={<TokenDetail />} />
          <Route path="/validators" element={<Validators />} />
          <Route path="/contract/:address/verify" element={<VerifyContract />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  )
}
