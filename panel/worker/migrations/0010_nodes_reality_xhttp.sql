-- 0010_nodes_reality_xhttp.sql
ALTER TABLE nodes ADD COLUMN reality_pubkey TEXT;
ALTER TABLE nodes ADD COLUMN reality_sid    TEXT;
ALTER TABLE nodes ADD COLUMN reality_sni    TEXT;
ALTER TABLE nodes ADD COLUMN reality_dest   TEXT;
ALTER TABLE nodes ADD COLUMN xhttp_path     TEXT;
