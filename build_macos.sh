#!/bin/bash

# Build script for macOS binary with optional UPX compression
# Usage: ./build_macos.sh [--compress]

set -e

PROJECT_NAME="grep_app_mcp"
OUTPUT_DIR="build"
BINARY_NAME="grep_app_mcp_darwin"

# Parse command line arguments
COMPRESS=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --compress)
            COMPRESS=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [--compress]"
            exit 1
            ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Building macOS binary for ${PROJECT_NAME}...${NC}"

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Build for macOS (darwin/amd64)
echo -e "${YELLOW}Building for macOS Intel (darwin/amd64)...${NC}"
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o "$OUTPUT_DIR/${BINARY_NAME}_amd64" .

# Build for macOS Apple Silicon (darwin/arm64)
echo -e "${YELLOW}Building for macOS Apple Silicon (darwin/arm64)...${NC}"
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o "$OUTPUT_DIR/${BINARY_NAME}_arm64" .

# Compress with UPX if requested
if [ "$COMPRESS" = true ]; then
    if command -v upx &> /dev/null; then
        echo -e "${YELLOW}Compressing binaries with UPX...${NC}"
        
        # Compress Intel binary
        echo -e "${YELLOW}Compressing Intel binary...${NC}"
        upx --best "$OUTPUT_DIR/${BINARY_NAME}_amd64" || echo -e "${RED}Failed to compress Intel binary${NC}"
        
        # Compress Apple Silicon binary
        echo -e "${YELLOW}Compressing Apple Silicon binary...${NC}"
        upx --best "$OUTPUT_DIR/${BINARY_NAME}_arm64" || echo -e "${RED}Failed to compress Apple Silicon binary${NC}"
        
        echo -e "${GREEN}UPX compression completed!${NC}"
    else
        echo -e "${RED}UPX not found. Install with: brew install upx${NC}"
        echo -e "${YELLOW}Skipping compression...${NC}"
    fi
fi

# Show file sizes
echo -e "${GREEN}Build completed! Files created:${NC}"
ls -lh "$OUTPUT_DIR"/${BINARY_NAME}_*

echo -e "${GREEN}Done!${NC}"