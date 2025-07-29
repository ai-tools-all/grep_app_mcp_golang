#!/bin/bash

# Cross-platform build script for Android and Linux binaries
# Usage: ./build_cross_platform.sh [all] [--compress]
# Default: builds only for Linux
# Use 'all' argument to build for all platforms

set -e

PROJECT_NAME="grep_app_mcp"
OUTPUT_DIR="build"
BINARY_NAME="grep_app_mcp"

# Get version information
VERSION=${VERSION:-"1.0.0"}
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%S%Z)
BUILD_USER=$(whoami)

# Build ldflags for version injection
LDFLAGS="-s -w -X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildDate=${BUILD_DATE} -X main.BuildBy=${BUILD_USER}"

# Parse command line arguments
COMPRESS=false
BUILD_ALL=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --compress)
            COMPRESS=true
            shift
            ;;
        all)
            BUILD_ALL=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [all] [--compress]"
            exit 1
            ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

if [ "$BUILD_ALL" = true ]; then
    echo -e "${GREEN}Building cross-platform binaries for ${PROJECT_NAME}...${NC}"
else
    echo -e "${GREEN}Building Linux binaries for ${PROJECT_NAME}...${NC}"
fi

echo -e "${BLUE}Version: ${VERSION}, Commit: ${GIT_COMMIT}, Date: ${BUILD_DATE}${NC}"

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Build for Linux (amd64) - Always build this
echo -e "${YELLOW}Building for Linux (linux/amd64)...${NC}"
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "$OUTPUT_DIR/${BINARY_NAME}_linux_amd64" .

# Only build additional platforms if 'all' is specified
if [ "$BUILD_ALL" = true ]; then
    # Build for Linux (arm64)
    echo -e "${YELLOW}Building for Linux ARM64 (linux/arm64)...${NC}"
    GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "$OUTPUT_DIR/${BINARY_NAME}_linux_arm64" .
fi

# Only build Android if 'all' is specified
if [ "$BUILD_ALL" = true ]; then
    # Check for Android NDK
    NDK_PATH=""
    if [ -d "/home/abhishek/Android/Sdk/ndk" ]; then
        NDK_PATH=$(find /home/abhishek/Android/Sdk/ndk -maxdepth 1 -type d | sort -V | tail -n 1)
        echo -e "${GREEN}Found NDK at: $NDK_PATH${NC}"
    elif [ -d "/home/abhishek/Android/Sdk/ndk-bundle" ]; then
        NDK_PATH="/home/abhishek/Android/Sdk/ndk-bundle"
        echo -e "${GREEN}Found NDK bundle at: $NDK_PATH${NC}"
    elif [ -n "$ANDROID_NDK_HOME" ]; then
        NDK_PATH="$ANDROID_NDK_HOME"
        echo -e "${GREEN}Using NDK from ANDROID_NDK_HOME: $NDK_PATH${NC}"
    fi

    if [ -n "$NDK_PATH" ]; then
        export ANDROID_NDK_HOME="$NDK_PATH"
        export CC="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang"
        export CXX="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang++"
        
        # Build for Android (arm64)
        echo -e "${YELLOW}Building for Android ARM64 (android/arm64)...${NC}"
        CGO_ENABLED=1 GOOS=android GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "$OUTPUT_DIR/${BINARY_NAME}_android_arm64" .
        
        # Build for Android (amd64) - for emulators  
        echo -e "${YELLOW}Building for Android x86_64 (android/amd64)...${NC}"
        export CC="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android21-clang"
        export CXX="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android21-clang++"
        CGO_ENABLED=1 GOOS=android GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "$OUTPUT_DIR/${BINARY_NAME}_android_amd64" .
        
        # Build for Android (arm) - 32-bit ARM
        echo -e "${YELLOW}Building for Android ARM (android/arm)...${NC}"
        export CC="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi21-clang"
        export CXX="$NDK_PATH/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi21-clang++"
        CGO_ENABLED=1 GOOS=android GOARCH=arm go build -ldflags="${LDFLAGS}" -o "$OUTPUT_DIR/${BINARY_NAME}_android_arm" .
    else
        echo -e "${RED}Android NDK not found. Skipping Android builds...${NC}"
        echo -e "${YELLOW}To build for Android, install Android NDK or set ANDROID_NDK_HOME${NC}"
    fi
fi

# Compress with UPX if requested
if [ "$COMPRESS" = true ]; then
    if command -v upx &> /dev/null; then
        echo -e "${YELLOW}Compressing binaries with UPX...${NC}"
        
        # Compress all binaries
        for binary in "$OUTPUT_DIR"/${BINARY_NAME}_*; do
            if [ -f "$binary" ]; then
                echo -e "${BLUE}Compressing $(basename "$binary")...${NC}"
                upx --best "$binary" || echo -e "${RED}Failed to compress $(basename "$binary")${NC}"
            fi
        done
        
        echo -e "${GREEN}UPX compression completed!${NC}"
    else
        echo -e "${RED}UPX not found. Install with: sudo apt install upx (Ubuntu/Debian) or brew install upx (macOS)${NC}"
        echo -e "${YELLOW}Skipping compression...${NC}"
    fi
fi

# Show file sizes
echo -e "${GREEN}Build completed! Files created:${NC}"
ls -lh "$OUTPUT_DIR"/${BINARY_NAME}_*

echo -e "${GREEN}Platform-specific binaries built:${NC}"
echo -e "${BLUE}Linux x86_64:${NC} $OUTPUT_DIR/${BINARY_NAME}_linux_amd64"

if [ "$BUILD_ALL" = true ]; then
    echo -e "${BLUE}Linux ARM64:${NC} $OUTPUT_DIR/${BINARY_NAME}_linux_arm64"
    if [ -n "$NDK_PATH" ]; then
        echo -e "${BLUE}Android ARM64:${NC} $OUTPUT_DIR/${BINARY_NAME}_android_arm64"
        echo -e "${BLUE}Android x86_64:${NC} $OUTPUT_DIR/${BINARY_NAME}_android_amd64"
        echo -e "${BLUE}Android ARM:${NC} $OUTPUT_DIR/${BINARY_NAME}_android_arm"
    else
        echo -e "${BLUE}Android ARM64:${NC} skipped (NDK not found)"
        echo -e "${BLUE}Android x86_64:${NC} skipped (NDK not found)"
        echo -e "${BLUE}Android ARM:${NC} skipped (NDK not found)"
    fi
else
    echo -e "${YELLOW}Other platforms skipped. Use 'all' argument to build for all platforms.${NC}"
fi

echo -e "${GREEN}Done!${NC}"