#/bin/sh
# ===== 1. 產生 Root CA =====
# Root CA private key
openssl genrsa -out rootCA.key 4096

# Root CA self-signed cert (10 years)
openssl req -x509 -new -nodes \
	  -key rootCA.key \
	    -sha256 \
	      -days 3650 \
	        -out rootCA.pem \
		  -subj "/CN=Oktopus-ACS-Root-CA/O=Compal Broadband Networks/C=TW"

# ===== 2. 產生 ACS Server Key + CSR =====
# Server private key
openssl genrsa -out acs_server.key 2048

# Server CSR
openssl req -new \
	  -key acs_server.key \
	    -out acs_server.csr \
	      -subj "/CN=172.16.0.3/O=Compal Broadband Networks/C=TW"

# ===== 3. 用 Root CA 簽發 Server Cert =====
# 先建立 extension 檔（SAN）
cat > acs_server_ext.cnf << EOF
[v3_req]
subjectAltName = @alt_names
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth

[alt_names]
IP.1 = 172.16.0.3
DNS.1 = acs
EOF

# 簽發 server cert (1 year)
openssl x509 -req \
	  -in acs_server.csr \
	    -CA rootCA.pem \
	      -CAkey rootCA.key \
	        -CAcreateserial \
		  -out acs_server.crt \
		    -days 365 \
		      -sha256 \
		        -extfile acs_server_ext.cnf \
			  -extensions v3_req

# ===== 4. 驗證 =====
openssl verify -CAfile rootCA.pem acs_server.crt
# 應輸出: acs_server.crt: OK

openssl x509 -in acs_server.crt -noout -text | grep -A2 "Subject Alternative"
# 確認 SAN 包含 IP:172.16.0.3
