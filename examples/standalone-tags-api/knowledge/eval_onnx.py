#!/usr/bin/env python3
"""Loopback-only evaluation bridge for an already cached EmbeddingGemma ONNX.

No downloads, credentials, external corpora or Hugging Face cache writes. This
helper is for local evaluation, not a production model-serving service.
"""
import argparse
import hashlib
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--snapshot', type=Path, required=True)
    parser.add_argument('--port', type=int, default=0)
    args = parser.parse_args()
    snapshot = args.snapshot.resolve(strict=True)
    model_id = f'embeddinggemma-300m-onnx-q8@{snapshot.name}'
    tokenizer = Tokenizer.from_file(str(snapshot / 'tokenizer.json'))
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    options.log_severity_level = 3
    session = ort.InferenceSession(str(snapshot / 'onnx/model_quantized.onnx'),
                                   options, providers=['CPUExecutionProvider'])

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # Never log document text or request bodies.

        def do_POST(self):
            try:
                size = int(self.headers.get('Content-Length', '0'))
                if self.path != '/api/embed' or not 0 < size <= 65536:
                    raise ValueError('invalid request')
                body = json.loads(self.rfile.read(size))
                if body.get('model') != model_id or body.get('truncate') is not False:
                    raise ValueError('model/truncation mismatch')
                text = body.get('input')
                if not isinstance(text, str) or not text:
                    raise ValueError('text required')
                ids = tokenizer.encode(text, add_special_tokens=True).ids
                if not 0 < len(ids) <= 2048:
                    raise ValueError('input exceeds model context')
                inputs = {'input_ids': np.array([ids], dtype=np.int64),
                          'attention_mask': np.ones((1, len(ids)), dtype=np.int64)}
                vector = session.run(['sentence_embedding'], inputs)[0][0]
                if vector.shape != (768,) or not np.isfinite(vector).all():
                    raise ValueError('invalid model output')
                norm = np.linalg.norm(vector)
                if not np.isfinite(norm) or norm <= 0:
                    raise ValueError('invalid norm')
                raw = json.dumps({'embeddings': [(vector / norm).tolist()]}).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
            except (ValueError, KeyError, TypeError):
                self.send_error(400, 'invalid embedding input')

    server = HTTPServer(('127.0.0.1', args.port), Handler)
    files = {}
    for name in ['tokenizer.json', 'onnx/model_quantized.onnx', 'onnx/model_quantized.onnx_data']:
        digest = hashlib.sha256()
        with (snapshot / name).open('rb') as handle:
            for part in iter(lambda: handle.read(1024 * 1024), b''):
                digest.update(part)
        files[name] = digest.hexdigest()
    print(json.dumps({'url': f'http://127.0.0.1:{server.server_port}/api/embed',
                      'model': model_id, 'dimensions': 768,
                      'onnxruntime': ort.__version__, 'numpy': np.__version__,
                      'sha256': files}), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == '__main__':
    main()
