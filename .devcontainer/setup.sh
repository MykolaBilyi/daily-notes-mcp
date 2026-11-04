#!/usr/bin/env bash

mise trust
mise install --yes --quiet 
echo eval "\$(mise activate bash)" >> ~/.bashrc
echo eval "\$(mise completion bash)" >> ~/.bashrc
