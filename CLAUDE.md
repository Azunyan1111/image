# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## プロジェクト概要

このリポジトリは `golang.org/x/image` の補助画像パッケージ集です。Go標準ライブラリの `image` パッケージを拡張し、追加の画像フォーマットやフォント処理機能を提供します。

## ビルドとテスト

```bash
# 全テスト実行
go test ./...

# 特定パッケージのテスト
go test ./bmp
go test ./tiff
go test ./webp

# 単一テスト実行
go test ./draw -run TestScaleDown
```

## アーキテクチャ

### 画像フォーマットパッケージ
各フォーマットパッケージ（bmp, tiff, webp等）は `image.RegisterFormat` を使用して `init()` でフォーマットを登録します。これにより `image.Decode()` で自動的にフォーマットを検出できます。

### 主要パッケージ構成
- **bmp, tiff, webp**: 画像フォーマットのエンコーダー/デコーダー
- **draw**: 画像合成機能（標準ライブラリ `image/draw` のスーパーセット）。スケーリングや補間処理を含む
- **font**: フォント描画インターフェース。`Face` インターフェースが中心
- **font/sfnt**: OpenType/TrueTypeフォントパーサー
- **font/opentype**: sfntパッケージのラッパーで、`font.Face` 実装を提供
- **vp8, vp8l**: WebPの内部コーデック実装
- **riff**: WebPで使用されるRIFFファイルフォーマットパーサー
- **ccitt**: TIFFで使用されるCCITT圧縮の実装
- **math/fixed**: 固定小数点演算（フォント描画で使用）
- **vector**: 2Dベクターラスタライザー

### コード生成
一部のパッケージには `gen.go` ファイルがあり、`go generate` でコードを生成します：
- `draw/impl.go` は `draw/gen.go` から生成
- `colornames/table.go` は `colornames/gen.go` から生成
- `font/basicfont/data.go` は `font/basicfont/gen.go` から生成

## 依存関係

唯一の外部依存は `golang.org/x/text` です。
